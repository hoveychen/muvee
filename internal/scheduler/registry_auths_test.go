package scheduler

import (
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

func TestBuildRegistryAuthsPayload(t *testing.T) {
	t.Run("maps fields and preserves order", func(t *testing.T) {
		in := []store.RegistryAuth{
			{Addr: "ghcr.io", Username: "alice", Password: "pw1"},
			{Addr: "quay.io", Username: "bob", Password: "pw2"},
		}
		got := buildRegistryAuthsPayload(in)
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0]["addr"] != "ghcr.io" || got[0]["username"] != "alice" || got[0]["password"] != "pw1" {
			t.Errorf("got[0] = %v", got[0])
		}
		if got[1]["addr"] != "quay.io" || got[1]["password"] != "pw2" {
			t.Errorf("got[1] = %v", got[1])
		}
	})

	t.Run("drops entries with empty addr", func(t *testing.T) {
		in := []store.RegistryAuth{
			{Addr: "", Username: "x", Password: "y"},
			{Addr: "ghcr.io", Username: "a", Password: "b"},
		}
		got := buildRegistryAuthsPayload(in)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0]["addr"] != "ghcr.io" {
			t.Errorf("got[0][addr] = %q, want ghcr.io", got[0]["addr"])
		}
	})

	t.Run("nil input yields empty, non-nil slice", func(t *testing.T) {
		got := buildRegistryAuthsPayload(nil)
		if got == nil {
			t.Error("got nil, want empty slice")
		}
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}
