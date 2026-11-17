package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProjectForwardAuth_PassesSessionCookieThrough pins the Traefik side of
// the sliding session: authservice renews the host-only __Host-muvee_fwd on a
// 2xx verify, and Traefik only copies that Set-Cookie onto the proxied
// response when the cookie is listed in addAuthCookiesToResponse. The legacy
// muvee_fwd_session stays listed through the migration (BACKLOG-20261117-007).
func TestProjectForwardAuth_PassesSessionCookieThrough(t *testing.T) {
	b, err := json.Marshal(projectForwardAuth("http://authservice:4181/verify?project_id=p"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"addAuthCookiesToResponse":["__Host-muvee_fwd","muvee_fwd","muvee_fwd_session"]`) {
		t.Fatalf("forwardAuth config missing session cookie passthrough: %s", b)
	}
}
