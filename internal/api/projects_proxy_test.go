package api

import (
	"net/http"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

// TestApplyForwardedUserHeaders_OverwritesSpoofed locks in that the project
// proxy overwrites the ENTIRE X-Forwarded-User-* set with the authenticated
// user's identity. `projects curl -H` lets a caller set arbitrary headers; if
// only X-Forwarded-User were set, a spoofed X-Forwarded-User-Name / -Avatar /
// -Provider would reach the container and be trusted as the identity.
func TestApplyForwardedUserHeaders_OverwritesSpoofed(t *testing.T) {
	h := http.Header{}
	h.Set("X-Forwarded-User", "attacker@evil.com")
	h.Set("X-Forwarded-User-Name", "Spoofed Admin")
	h.Set("X-Forwarded-User-Avatar", "http://evil.example/a.png")
	h.Set("X-Forwarded-User-Provider", "google")

	user := &store.User{
		Email:     "real@example.com",
		Name:      "Real User",
		AvatarURL: "http://ok.example/a.png",
	}
	applyForwardedUserHeaders(h, user)

	if got := h.Get("X-Forwarded-User"); got != "real@example.com" {
		t.Errorf("X-Forwarded-User = %q, want real@example.com", got)
	}
	if got := h.Get("X-Forwarded-User-Name"); got != "Real User" {
		t.Errorf("X-Forwarded-User-Name = %q, want %q — spoofed value must be overwritten", got, "Real User")
	}
	if got := h.Get("X-Forwarded-User-Avatar"); got != "http://ok.example/a.png" {
		t.Errorf("X-Forwarded-User-Avatar = %q, want the real avatar — spoofed value must be overwritten", got)
	}
	if got := h.Get("X-Forwarded-User-Provider"); got != "" {
		t.Errorf("X-Forwarded-User-Provider = %q, want empty — CLI proxy identity has no provider, spoofed value must be removed", got)
	}
}
