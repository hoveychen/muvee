package mesh

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Spec is what the deployer needs to attach a project container to the mesh.
type Spec struct {
	Network     string
	GatewayIP   string
	FallbackDNS []string
}

// Gateway owns the mesh network attachment and the DNS/HTTP listeners.
type Gateway struct {
	cfg Config

	mu   sync.Mutex
	spec *Spec // non-nil only while the gateway is fully up
}

const reconcileInterval = 30 * time.Second

// Start launches the gateway in the background and never fails: when the
// agent is not inside a container, or docker refuses, mesh stays disabled
// (Spec returns nil) and the agent keeps running. The reconcile loop re-attaches
// the agent to the network if it gets disconnected or the network is removed.
func Start(ctx context.Context, cfg Config) *Gateway {
	g := &Gateway{cfg: cfg}
	go g.run(ctx)
	return g
}

// Spec returns the attachment spec for new deployments, or nil when the
// gateway is not up (deployments then skip the mesh entirely).
func (g *Gateway) Spec() *Spec {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spec
}

func (g *Gateway) setSpec(s *Spec) {
	g.mu.Lock()
	g.spec = s
	g.mu.Unlock()
}

func (g *Gateway) run(ctx context.Context) {
	self := g.cfg.SelfContainer
	if self == "" {
		self = detectSelfContainer(ctx)
	}
	if self == "" {
		log.Printf("mesh: agent is not running inside a docker container; muvee-mesh gateway disabled (set MESH_SELF_CONTAINER to override)")
		return
	}
	upstreams := readResolvConf("/etc/resolv.conf").upstreams()
	if len(upstreams) == 0 {
		log.Printf("mesh: no nameserver in /etc/resolv.conf; non-mesh DNS queries will fail")
	}
	fallback := g.cfg.FallbackDNS
	if len(fallback) == 0 {
		fallback = probeDefaultBridgeDNS(ctx, containerImage(ctx, self))
	}
	if len(fallback) == 0 {
		fallback = readResolvConf("/etc/resolv.conf").fallbackDNS()
	}
	if len(fallback) == 0 {
		log.Printf("mesh: could not determine a fallback DNS for project containers (set MESH_FALLBACK_DNS); they will depend on the gateway for all DNS")
	}

	callers := newDockerCallers()
	go callers.watch(ctx)
	l := &listeners{
		dns:   func(ip string) dns.Handler { return newDNSHandler(ip, upstreams) },
		proxy: newProxy(callers, newCachedResolver(newHTTPResolver(g.cfg.ControlPlaneURL, g.cfg.AgentSecret), 5*time.Second)),
	}
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		gwIP, err := g.reconcile(ctx, self, l)
		if err != nil {
			log.Printf("mesh: %v", err)
			g.setSpec(nil)
		} else {
			if g.Spec() == nil {
				log.Printf("mesh: gateway up on %s (network %s, fallback dns %v)", gwIP, NetworkName, fallback)
			}
			g.setSpec(&Spec{Network: NetworkName, GatewayIP: gwIP, FallbackDNS: fallback})
		}
		select {
		case <-ctx.Done():
			l.stop()
			return
		case <-ticker.C:
		}
	}
}

func (g *Gateway) reconcile(ctx context.Context, self string, l *listeners) (string, error) {
	desired := g.cfg.Subnet
	if desired == "" {
		desired = defaultSubnet
	}
	desiredGW, err := pickGatewayIP(desired, g.cfg.GatewayIP)
	if err != nil {
		return "", err
	}
	subnet, err := ensureNetwork(ctx, desired, desiredGW)
	if err != nil {
		return "", err
	}
	gwIP, err := pickGatewayIP(subnet, g.cfg.GatewayIP)
	if err != nil {
		return "", err
	}
	if err := ensureSelfConnected(ctx, self, gwIP); err != nil {
		return "", err
	}
	if err := l.ensure(gwIP); err != nil {
		return "", err
	}
	return gwIP, nil
}

// listeners binds the DNS and HTTP servers to the gateway IP, rebinding when
// the IP changes.
type listeners struct {
	dns   func(ip string) dns.Handler
	proxy http.Handler

	ip      string
	stopDNS func()
	httpSrv *http.Server
}

func (l *listeners) ensure(ip string) error {
	if l.ip == ip {
		return nil
	}
	l.stop()
	stopDNS, err := serveDNS(net.JoinHostPort(ip, "53"), l.dns(ip))
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "80"))
	if err != nil {
		stopDNS()
		return err
	}
	srv := &http.Server{Handler: l.proxy, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("mesh http server stopped: %v", err)
		}
	}()
	l.ip, l.stopDNS, l.httpSrv = ip, stopDNS, srv
	return nil
}

func (l *listeners) stop() {
	if l.stopDNS != nil {
		l.stopDNS()
	}
	if l.httpSrv != nil {
		_ = l.httpSrv.Close()
	}
	l.ip, l.stopDNS, l.httpSrv = "", nil, nil
}
