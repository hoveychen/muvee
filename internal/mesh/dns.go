package mesh

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const meshDNSTTL = 30

// dnsHandler answers *.muvee.internal with the gateway IP and forwards every
// other query to the agent's upstream resolvers.
type dnsHandler struct {
	gatewayIP net.IP
	upstreams []string // host:port
	udp       *dns.Client
	tcp       *dns.Client
}

func newDNSHandler(gatewayIP string, upstreams []string) *dnsHandler {
	return &dnsHandler{
		gatewayIP: net.ParseIP(gatewayIP).To4(),
		upstreams: upstreams,
		udp:       &dns.Client{Net: "udp", Timeout: 3 * time.Second},
		tcp:       &dns.Client{Net: "tcp", Timeout: 5 * time.Second},
	}
}

// inZone reports whether name (FQDN or not) is muvee.internal or below it.
func inZone(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	return name == Zone || strings.HasSuffix(name, "."+Zone)
}

func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 1 && inZone(req.Question[0].Name) {
		_ = w.WriteMsg(h.answerLocal(req))
		return
	}
	_ = w.WriteMsg(h.forward(req, w.RemoteAddr().Network() == "tcp"))
}

// answerLocal returns an A record for A/ANY queries and an empty NOERROR
// (NODATA) for every other type, so clients asking AAAA first fall back to A.
func (h *dnsHandler) answerLocal(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true
	q := req.Question[0]
	if q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY {
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: meshDNSTTL},
			A:   h.gatewayIP,
		})
	}
	return resp
}

func (h *dnsHandler) forward(req *dns.Msg, overTCP bool) *dns.Msg {
	client := h.udp
	if overTCP {
		client = h.tcp
	}
	for _, up := range h.upstreams {
		// A truncated UDP answer is returned as-is; the client retries over TCP.
		resp, _, err := client.Exchange(req, up)
		if err == nil && resp != nil {
			return resp
		}
		log.Printf("mesh dns: upstream %s failed for %v: %v", up, req.Question, err)
	}
	fail := new(dns.Msg)
	fail.SetRcode(req, dns.RcodeServerFailure)
	return fail
}

// serveDNS starts UDP and TCP DNS servers on addr. It returns once both
// sockets are bound (or the first bind error).
func serveDNS(addr string, h dns.Handler) (shutdown func(), err error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		pc.Close()
		return nil, err
	}
	udpSrv := &dns.Server{PacketConn: pc, Handler: h}
	tcpSrv := &dns.Server{Listener: ln, Handler: h}
	go func() {
		if err := udpSrv.ActivateAndServe(); err != nil {
			log.Printf("mesh dns udp server stopped: %v", err)
		}
	}()
	go func() {
		if err := tcpSrv.ActivateAndServe(); err != nil {
			log.Printf("mesh dns tcp server stopped: %v", err)
		}
	}()
	return func() {
		_ = udpSrv.Shutdown()
		_ = tcpSrv.Shutdown()
	}, nil
}
