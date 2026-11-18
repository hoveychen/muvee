package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestCLILoginRejectsNonNumericPort(t *testing.T) {
	s := &Server{}
	for _, p := range []string{"", "abc", "0", "65536", "8080</script><script>alert(1)</script>", "80 "} {
		rec := httptest.NewRecorder()
		s.handleCLILogin(rec, httptest.NewRequest(http.MethodGet, "/auth/cli/login?port="+url.QueryEscape(p), nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("port %q: want 400, got %d", p, rec.Code)
		}
	}
}

func TestPendingLoginExpires(t *testing.T) {
	s := &Server{}
	s.oauthPending.Store("old", oauthPendingEntry{Provider: "google", Created: time.Now().Add(-pendingLoginTTL - time.Second)})
	s.cliPending.Store("oldcli", cliPendingEntry{Port: 1, Created: time.Now().Add(-pendingLoginTTL - time.Second)})
	s.storeOAuthPending("new", "google")
	if _, ok := s.oauthPending.Load("old"); ok {
		t.Fatal("expired oauth state not evicted")
	}
	if _, ok := s.cliPending.Load("oldcli"); ok {
		t.Fatal("expired cli state not evicted")
	}
	if _, ok := s.oauthPending.Load("new"); !ok {
		t.Fatal("new state missing")
	}
}
