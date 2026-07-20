package api

import "testing"

// TestAllowedOriginMultiDomain locks in the P5 contract for the admin API:
// the panel must accept the canonical https://<base> origin of EVERY
// configured base domain, not just the first. Before multi-domain the check
// only compared against s.baseDomain, so a panel served on muvee.ai would have
// its own XHRs rejected by CORS.
func TestAllowedOriginMultiDomain(t *testing.T) {
	s := &Server{
		baseDomain:  "muveeai.com",
		baseDomains: []string{"muveeai.com", "muvee.ai"},
	}

	if !s.allowedOrigin("https://muveeai.com") {
		t.Error("canonical base origin should be allowed")
	}
	if !s.allowedOrigin("https://muvee.ai") {
		t.Error("second base-domain origin should be allowed under multi-domain")
	}
	if s.allowedOrigin("https://evil.example.org") {
		t.Error("unrelated origin must be rejected")
	}
}

// TestAllowedOriginSingleDomain guards the single-domain path: only the one
// canonical origin is accepted.
func TestAllowedOriginSingleDomain(t *testing.T) {
	s := &Server{baseDomain: "example.com"}
	if !s.allowedOrigin("https://example.com") {
		t.Error("canonical origin should be allowed")
	}
	if s.allowedOrigin("https://muvee.ai") {
		t.Error("non-configured origin must be rejected in single-domain mode")
	}
}
