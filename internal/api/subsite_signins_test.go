package api

import (
	"os"
	"strings"
	"testing"
)

// The subsite sign-ins endpoints live in the authenticated /api/me group
// (routes are registered inline in the router, so assert on the source).
func TestSubsiteSignins_RoutesRegistered(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`r.Post("/api/me/fwd-sessions/revoke", s.handleRevokeFwdSessions)`,
		`r.Get("/api/me/visited-projects", s.handleListVisitedProjects)`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("route not registered: %s", want)
		}
	}
	// Same authenticated group as the CLI-token revoke button.
	cli := strings.Index(string(src), `r.Post("/api/me/cli-tokens/revoke"`)
	rev := strings.Index(string(src), `r.Post("/api/me/fwd-sessions/revoke"`)
	if cli < 0 || rev < cli || rev-cli > 600 {
		t.Errorf("fwd-sessions revoke is not registered next to cli-tokens revoke (auth group)")
	}
}
