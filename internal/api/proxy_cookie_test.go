package api

import (
	"net/http"
	"testing"
)

func TestStripPlatformCookies(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "muvee_session=jwt; app_sid=abc; oauth_state=g:s; muvee_invite_token=t; muvee_post_login_redirect=/x")
	stripPlatformCookies(h)
	if got := h.Get("Cookie"); got != "app_sid=abc" {
		t.Fatalf("got %q", got)
	}
	h2 := http.Header{}
	h2.Set("Cookie", "muvee_session=jwt")
	stripPlatformCookies(h2)
	if _, ok := h2["Cookie"]; ok {
		t.Fatalf("cookie header should be gone, got %q", h2.Get("Cookie"))
	}
}
