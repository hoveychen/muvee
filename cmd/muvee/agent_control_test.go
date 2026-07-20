package main

import (
	"context"
	"testing"

	"github.com/hoveychen/muvee/internal/agentcontrol"
)

// TestContainerForFrame_ComposeUsesResolvedName reproduces the exec/cp bug:
// for compose projects the control plane sends the naive "muvee-<prefix>"
// container name, but the real container is "<project>-<service>-N" (e.g.
// "muvee-pixel-app-1"). The agent must resolve the real name from the domain
// prefix via the docker label instead of trusting the literal Container field.
func TestContainerForFrame_ComposeUsesResolvedName(t *testing.T) {
	orig := resolveExecContainer
	defer func() { resolveExecContainer = orig }()
	resolveExecContainer = func(ctx context.Context, prefix string) string {
		if prefix == "pixel" {
			return "muvee-pixel-app-1"
		}
		return "muvee-" + prefix
	}

	f := agentcontrol.Frame{
		Type:         agentcontrol.TypeOpenExec,
		Container:    "muvee-pixel", // naive name the control plane hardcodes
		DomainPrefix: "pixel",
	}
	got := containerForFrame(context.Background(), f)
	if got != "muvee-pixel-app-1" {
		t.Errorf("containerForFrame = %q, want %q (real compose container)", got, "muvee-pixel-app-1")
	}
}

// TestContainerForFrame_LegacyFallback verifies backward compatibility: when a
// frame carries no domain prefix (older control plane), the agent falls back to
// the literal Container field.
func TestContainerForFrame_LegacyFallback(t *testing.T) {
	orig := resolveExecContainer
	defer func() { resolveExecContainer = orig }()
	resolveExecContainer = func(ctx context.Context, prefix string) string {
		t.Fatalf("resolver must not be called when DomainPrefix is empty")
		return ""
	}

	f := agentcontrol.Frame{
		Type:      agentcontrol.TypeOpenExec,
		Container: "muvee-legacy",
	}
	got := containerForFrame(context.Background(), f)
	if got != "muvee-legacy" {
		t.Errorf("containerForFrame = %q, want %q (legacy fallback)", got, "muvee-legacy")
	}
}
