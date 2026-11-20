package api

import (
	"testing"

	"github.com/hoveychen/muvee/internal/agentcontrol"
)

func TestSanitizeCLIFollowupFrameRejectsReopen(t *testing.T) {
	for _, typ := range []string{agentcontrol.TypeOpenExec, agentcontrol.TypeOpenCp, agentcontrol.TypeHello, "bogus"} {
		if _, ok := sanitizeCLIFollowupFrame(agentcontrol.Frame{Type: typ, Container: "muvee-victim", Cmd: []string{"sh"}}, "s1"); ok {
			t.Fatalf("type %q must be rejected mid-session", typ)
		}
	}
}

func TestSanitizeCLIFollowupFrameStripsTargeting(t *testing.T) {
	in := agentcontrol.Frame{
		Type: agentcontrol.TypeStdio, Session: "attacker", Container: "muvee-victim",
		DomainPrefix: "victim", Cmd: []string{"sh"}, Path: "/etc", Direction: "down",
		NodeID: "n", Data: []byte("ls\n"), Stream: "stdin",
	}
	out, ok := sanitizeCLIFollowupFrame(in, "s1")
	if !ok {
		t.Fatal("stdio must pass")
	}
	if out.Session != "s1" || out.Container != "" || out.DomainPrefix != "" || out.Cmd != nil ||
		out.Path != "" || out.Direction != "" || out.NodeID != "" {
		t.Fatalf("targeting fields leaked: %+v", out)
	}
	if string(out.Data) != "ls\n" || out.Stream != "stdin" {
		t.Fatalf("payload lost: %+v", out)
	}
}
