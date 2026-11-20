package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// BACKLOG-20261121-001 P3: --account-badge sets the project's account_badge
// switch; left out, the body does not carry it (create: the server's default,
// on; update: unchanged).
func TestAccountBadgeFlag(t *testing.T) {
	newCmd := func(args ...string) map[string]interface{} {
		t.Helper()
		c := &cobra.Command{Use: "x"}
		addProjectFlags(c)
		if err := c.Flags().Parse(args); err != nil {
			t.Fatal(err)
		}
		return collectProjectFlags(c)
	}
	if _, ok := newCmd()["account_badge"]; ok {
		t.Fatal("no flag: body must not carry account_badge")
	}
	if v, ok := newCmd("--account-badge")["account_badge"]; !ok || v != true {
		t.Fatalf("--account-badge: account_badge = %v, %v", v, ok)
	}
	if v, ok := newCmd("--account-badge=false")["account_badge"]; !ok || v != false {
		t.Fatalf("--account-badge=false: account_badge = %v, %v", v, ok)
	}
}
