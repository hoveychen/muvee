package main

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hoveychen/muvee/internal/auth"
)

// TestOAuthRedirectFollowsRequestHost locks in the P3 multi-domain contract:
// the OAuth redirect_uri handed to the provider must be rebased onto whichever
// configured base domain the request arrived on, so a login started on
// muvee.ai comes back to app.muvee.ai (and drops its session cookie there)
// instead of bouncing through the canonical muveeai.com host. Uses the
// device-activate entry point with a Feishu provider because Feishu embeds
// redirect_uri as a plain query param in the authorize URL, making the
// generated value directly assertable without a live token exchange.
func TestOAuthRedirectFollowsRequestHost(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret_test")

	prevBase, prevDomain, prevBases := forwardAuthBase, cookieDomain, cookieBaseDomains
	forwardAuthBase = "https://app.muveeai.com"
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, cookieBaseDomains = prevBase, prevDomain, prevBases
	})

	provs, err := auth.NewForwardAuthProviders(forwardAuthBase)
	if err != nil {
		t.Fatalf("build providers: %v", err)
	}
	if _, ok := provs["feishu"]; !ok {
		t.Fatalf("feishu provider not built; got %v", provs)
	}
	prev := fwdProvidersAtomic.Load()
	fwdProvidersAtomic.Store(&provs)
	t.Cleanup(func() { fwdProvidersAtomic.Store(prev) })

	const userCode = "TESTCODE"
	deviceByUser.Store(userCode, "devicecode")
	t.Cleanup(func() { deviceByUser.Delete(userCode) })

	req := httptest.NewRequest("GET",
		"https://app.muvee.ai/_oauth/device/activate?code="+userCode+"&provider=feishu", nil)
	req.Host = "app.muvee.ai"
	req.Header.Set("X-Forwarded-Host", "app.muvee.ai")
	w := httptest.NewRecorder()

	handleDeviceActivate(w, req)

	loc := w.Result().Header.Get("Location")
	if loc == "" {
		t.Fatalf("no redirect; status=%d body=%s", w.Code, w.Body.String())
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	redirectURI := u.Query().Get("redirect_uri")
	ru, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri %q: %v", redirectURI, err)
	}
	if ru.Host != "app.muvee.ai" {
		t.Errorf("redirect_uri host = %q, want app.muvee.ai (redirect_uri=%q)", ru.Host, redirectURI)
	}
}
