package mesh

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// proxy is the gateway's HTTP handler: Host <prefix>.muvee.internal selects
// the target, the source IP selects the caller.
type proxy struct {
	callers  callerLookup
	resolver resolver
	rp       *httputil.ReverseProxy
}

type proxyCtxKey struct{}

type proxyTarget struct {
	backend string
	res     *Resolution
}

func newProxy(callers callerLookup, res resolver) *proxy {
	p := &proxy{callers: callers, resolver: res}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(proxyCtxKey{}).(proxyTarget)
			pr.SetURL(&url.URL{Scheme: "http", Host: t.backend})
			pr.Out.Host = pr.In.Host
			stripIdentityHeaders(pr.Out.Header)
			pr.Out.Header.Set(HeaderCallerProject, t.res.CallerProjectName)
			pr.Out.Header.Set(HeaderCallerProjectID, t.res.CallerProjectID)
		},
		// Flush immediately so SSE / chunked streams are not buffered.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("mesh proxy: %s %s: %v", r.Host, r.URL.Path, err)
			http.Error(w, "muvee mesh: target unreachable", http.StatusBadGateway)
		},
	}
	return p
}

// stripIdentityHeaders drops inbound headers a caller could use to spoof an
// identity: muvee's caller headers and the forwardAuth user headers.
func stripIdentityHeaders(h http.Header) {
	for k := range h {
		ck := http.CanonicalHeaderKey(k)
		if strings.HasPrefix(ck, "X-Forwarded-User") || strings.HasPrefix(ck, "X-Muvee-Caller-") {
			delete(h, k)
		}
	}
}

// targetPrefix extracts <prefix> from a Host of the form
// <prefix>.muvee.internal[:port]; ok is false for anything else.
func targetPrefix(host string) (string, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	prefix, ok := strings.CutSuffix(host, "."+Zone)
	if !ok || prefix == "" || strings.Contains(prefix, ".") {
		return "", false
	}
	return prefix, true
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, ok := targetPrefix(r.Host)
	if !ok {
		http.Error(w, "muvee mesh: host must be <project>."+Zone, http.StatusNotFound)
		return
	}
	srcIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		srcIP = r.RemoteAddr
	}
	caller := p.callers.Lookup(r.Context(), srcIP)
	if caller == "" {
		http.Error(w, "muvee mesh: caller is not a muvee project container", http.StatusForbidden)
		return
	}
	res, err := p.resolver.Resolve(r.Context(), caller, target)
	if err != nil {
		log.Printf("mesh proxy: resolve %s -> %s: %v", caller, target, err)
		http.Error(w, "muvee mesh: control plane unavailable", http.StatusBadGateway)
		return
	}
	if !res.Allowed {
		http.Error(w, "muvee mesh: "+res.Reason, statusForReason(res.Reason))
		return
	}
	if res.Backend == "" {
		http.Error(w, "muvee mesh: "+ReasonTargetNotRunning, http.StatusServiceUnavailable)
		return
	}
	ctx := context.WithValue(r.Context(), proxyCtxKey{}, proxyTarget{backend: res.Backend, res: res})
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}
