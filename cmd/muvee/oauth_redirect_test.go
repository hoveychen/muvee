package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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

	// -007: the device flow no longer leaves for the provider from the
	// subsite; activate hands off to the central sign-in host of the request's
	// base domain, whose /_oauth/start builds the provider URL. The contract
	// under test is unchanged: the redirect_uri follows the request's base
	// domain (app.muvee.ai), never the canonical app.muveeai.com.
	const userCode, deviceCode, subsite = "TESTCODE", "devicecode", "proj.muvee.ai"
	deviceFlows.Store(deviceCode, &deviceFlowEntry{
		UserCode: userCode, Aud: "project:p1", Host: subsite,
		ExpiresAt: time.Now().Add(time.Minute),
	})
	deviceByUser.Store(userCode, deviceCode)
	t.Cleanup(func() { deviceByUser.Delete(userCode); deviceFlows.Delete(deviceCode) })

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/internal/projects/by-host" && r.URL.Query().Get("host") == subsite {
			_, _ = w.Write([]byte(`{"project_id":"p1","aud":"project:p1"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(stub.Close)
	prevServer := muveeServerURL
	muveeServerURL = stub.URL
	t.Cleanup(func() { muveeServerURL = prevServer })

	req := httptest.NewRequest("GET",
		"https://"+subsite+"/_oauth/device/activate?code="+userCode+"&provider=feishu", nil)
	req.Host = subsite
	req.Header.Set("X-Forwarded-Host", subsite)
	req.Header.Set(routeAudienceHeader, "project:p1")
	w := httptest.NewRecorder()

	handleDeviceActivate(w, req)

	hop := w.Result().Header.Get("Location")
	if hop == "" {
		t.Fatalf("no redirect; status=%d body=%s", w.Code, w.Body.String())
	}
	hu, err := url.Parse(hop)
	if err != nil {
		t.Fatalf("parse Location %q: %v", hop, err)
	}
	if hu.Host != "app.muvee.ai" || hu.Path != "/_oauth/start" {
		t.Fatalf("activate hand-off = %q, want https://app.muvee.ai/_oauth/start", hop)
	}

	req = httptest.NewRequest("GET", hop, nil)
	req.Host = hu.Host
	req.Header.Set("X-Forwarded-Host", hu.Host)
	w = httptest.NewRecorder()
	handleSSOStart(w, req)

	loc := w.Result().Header.Get("Location")
	if loc == "" {
		t.Fatalf("central start: no redirect; status=%d body=%s", w.Code, w.Body.String())
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
