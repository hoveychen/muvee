package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// BACKLOG-20261121-001: enabling auth from the CLI reminds the user to give
// visitors a sign-out entry (/_oauth/me).
func TestAuthSignOutHint(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "x"}
		addProjectFlags(c)
		return c
	}

	c := newCmd()
	if got := authSignOutHint(c); got != "" {
		t.Fatalf("no flag: want no hint, got %q", got)
	}

	c = newCmd()
	if err := c.Flags().Set("auth-required", "true"); err != nil {
		t.Fatal(err)
	}
	got := authSignOutHint(c)
	if !strings.Contains(got, `<a href="/_oauth/me">`) || !strings.Contains(got, "service-auth-integration") {
		t.Fatalf("--auth-required: hint must carry the /_oauth/me snippet and docs link, got %q", got)
	}

	c = newCmd()
	if err := c.Flags().Set("no-auth", "true"); err != nil {
		t.Fatal(err)
	}
	if got := authSignOutHint(c); got != "" {
		t.Fatalf("--no-auth: want no hint, got %q", got)
	}
}
