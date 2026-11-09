package mesh

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

// startDNS serves h on a random localhost UDP+TCP port pair sharing one port.
func startDNS(t *testing.T, h dns.Handler) string {
	t.Helper()
	for i := 0; i < 10; i++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := pc.LocalAddr().String()
		pc.Close()
		stop, err := serveDNS(addr, h)
		if err != nil {
			continue // port grabbed in between; retry
		}
		t.Cleanup(stop)
		return addr
	}
	t.Fatal("could not bind dns test server")
	return ""
}

func query(t *testing.T, network, server, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := (&dns.Client{Net: network}).Exchange(m, server)
	if err != nil {
		t.Fatalf("query %s %s over %s: %v", name, dns.TypeToString[qtype], network, err)
	}
	return resp
}

func TestDNSAnswersMeshZoneAndForwardsTheRest(t *testing.T) {
	upstream := startDNS(t, dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if r.Question[0].Name == "example.com." {
			rr, _ := dns.NewRR("example.com. 60 IN A 93.184.216.34")
			m.Answer = append(m.Answer, rr)
		} else {
			m.Rcode = dns.RcodeNameError
		}
		w.WriteMsg(m)
	}))
	gw := startDNS(t, newDNSHandler("10.213.0.2", []string{upstream}))

	for _, network := range []string{"udp", "tcp"} {
		resp := query(t, network, gw, "foxy.muvee.internal", dns.TypeA)
		if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 || resp.Answer[0].(*dns.A).A.String() != "10.213.0.2" {
			t.Fatalf("%s: mesh A answer wrong: %v", network, resp)
		}
		resp = query(t, network, gw, "FOXY.Muvee.Internal", dns.TypeAAAA)
		if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 {
			t.Fatalf("%s: AAAA must be NODATA: %v", network, resp)
		}
		resp = query(t, network, gw, "example.com", dns.TypeA)
		if len(resp.Answer) != 1 || resp.Answer[0].(*dns.A).A.String() != "93.184.216.34" {
			t.Fatalf("%s: forwarded answer wrong: %v", network, resp)
		}
		resp = query(t, network, gw, "nope.example", dns.TypeA)
		if resp.Rcode != dns.RcodeNameError {
			t.Fatalf("%s: upstream NXDOMAIN must pass through: %v", network, resp)
		}
	}
}

func TestDNSServfailWhenUpstreamsDown(t *testing.T) {
	gw := startDNS(t, newDNSHandler("10.213.0.2", []string{"127.0.0.1:1"}))
	if resp := query(t, "udp", gw, "example.com", dns.TypeA); resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("want SERVFAIL, got %v", resp)
	}
	// The mesh zone is still answered locally.
	if resp := query(t, "udp", gw, "x.muvee.internal", dns.TypeA); len(resp.Answer) != 1 {
		t.Fatalf("mesh zone must not depend on upstream: %v", resp)
	}
}

func TestInZone(t *testing.T) {
	for name, want := range map[string]bool{
		"a.muvee.internal.": true, "muvee.internal": true, "A.MUVEE.INTERNAL": true,
		"amuvee.internal": false, "muvee.internal.com": false, "example.com.": false,
	} {
		if inZone(name) != want {
			t.Errorf("inZone(%q) != %v", name, want)
		}
	}
}
