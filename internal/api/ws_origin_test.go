package api

import (
	"net/http/httptest"
	"testing"
)

func TestWSOriginAllowed(t *testing.T) {
	s := &Server{baseDomain: "muveeai.com", baseDomains: []string{"muveeai.com"}}
	cases := map[string]bool{
		"":                         true,  // CLI / agent: no Origin header
		"https://muveeai.com":      true,  // panel
		"https://evil.muveeai.com": false, // same-site tenant page
		"https://attacker.example": false,
		"http://muveeai.com":       false,
	}
	for origin, want := range cases {
		r := httptest.NewRequest("GET", "/api/projects/x/exec", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if got := s.wsOriginAllowed(r); got != want {
			t.Errorf("origin %q: got %v want %v", origin, got, want)
		}
	}
}
