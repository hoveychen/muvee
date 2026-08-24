package api

import (
	"net/http"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

// The project proxy forwards the same identity header set as Traefik's
// forward-auth, so it carries the same hazard: a non-ASCII value is deprecated
// obs-text that a strict upstream rejects at the parser, taking the whole
// request down rather than just the header.
//
// This is the second surface of the bug fixed in authservice's setUserHeaders
// (fleet-cloud 502'd on every request after a Feishu login because the Chinese
// display name went out raw and `tiny_http` requires ASCII header values). The
// forward-auth path and this proxy path both inject the name, so fixing only
// one leaves `muveectl projects curl` and the dashboard's embedded view broken
// against the same containers.
func TestApplyForwardedUserHeaders_ASCIIOnly(t *testing.T) {
	h := http.Header{}
	applyForwardedUserHeaders(h, &store.User{
		Email:     "boss@example.com",
		Name:      "陈昊维",
		AvatarURL: "https://example.com/头像.png",
	})
	for name, values := range h {
		for _, v := range values {
			for i := 0; i < len(v); i++ {
				if v[i] > 0x7f {
					t.Errorf("%s carries a non-ASCII byte %#x: %q", name, v[i], v)
					break
				}
			}
		}
	}
	if got := h.Get("X-Forwarded-User-Name"); got != "%E9%99%88%E6%98%8A%E7%BB%B4" {
		t.Errorf("X-Forwarded-User-Name = %q, want percent-encoded UTF-8", got)
	}
}

// An ASCII identity must pass through byte-identical, so nothing that works
// today changes.
func TestApplyForwardedUserHeaders_KeepsASCIIVerbatim(t *testing.T) {
	h := http.Header{}
	applyForwardedUserHeaders(h, &store.User{
		Email:     "boss@example.com",
		Name:      "Hovey Chen",
		AvatarURL: "https://example.com/a.png",
	})
	for header, want := range map[string]string{
		"X-Forwarded-User":        "boss@example.com",
		"X-Forwarded-User-Name":   "Hovey Chen",
		"X-Forwarded-User-Avatar": "https://example.com/a.png",
	} {
		if got := h.Get(header); got != want {
			t.Errorf("%s = %q, want %q verbatim", header, got, want)
		}
	}
}
