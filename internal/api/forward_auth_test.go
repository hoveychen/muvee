package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProjectForwardAuth_PassesSessionCookieThrough pins the Traefik side of
// the sliding session: authservice renews muvee_fwd_session on a 2xx verify,
// and Traefik only copies that Set-Cookie onto the proxied response when the
// cookie is listed in addAuthCookiesToResponse.
func TestProjectForwardAuth_PassesSessionCookieThrough(t *testing.T) {
	b, err := json.Marshal(projectForwardAuth("http://authservice:4181/verify?project_id=p"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"addAuthCookiesToResponse":["muvee_fwd_session"]`) {
		t.Fatalf("forwardAuth config missing session cookie passthrough: %s", b)
	}
}
