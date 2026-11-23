package embed_bridge

// BACKLOG-20261121-001 P3: the per-subsite account badge — a small link to the
// host's own /_oauth/me. The plugin only marks the injected <script> tag on a
// top-level page load that passed ForwardAuth; whether the badge shows is
// decided by authservice (/_oauth/account-ui), so the plugin needs no config
// of its own for it.

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const badgeHTML = `<!doctype html><html><head><title>Hi</title></head><body>x</body></html>`

func spliceFor(t *testing.T, hdr map[string]string) string {
	t.Helper()
	next := &fakeNext{contentType: "text/html; charset=utf-8", statusCode: http.StatusOK, body: badgeHTML}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	newPlugin(t, next).ServeHTTP(rec, req)
	return rec.Body.String()
}

const (
	plainTag  = `<script src="/_embed-bridge.js" defer></script></head>`
	markedTag = `<script src="/_embed-bridge.js" defer data-muvee-account></script></head>`
)

func TestAccountMark_TopLevelProtectedDocument(t *testing.T) {
	body := spliceFor(t, map[string]string{
		"Sec-Fetch-Dest":   "document",
		"X-Forwarded-User": "someone@corp.test",
	})
	if !strings.Contains(body, markedTag) {
		t.Fatalf("top-level protected page not marked:\n%s", body)
	}
	// The identity never goes into the page: it may be cached by a CDN.
	if strings.Contains(body, "someone@corp.test") {
		t.Fatalf("identity leaked into HTML:\n%s", body)
	}
}

func TestAccountMark_NotMarkedElsewhere(t *testing.T) {
	cases := map[string]map[string]string{
		// iframe (agent-workspace /embed etc.): never a badge.
		"iframe": {"Sec-Fetch-Dest": "iframe", "X-Forwarded-User": "u@corp.test"},
		"nested": {"Sec-Fetch-Dest": "nested-document", "X-Forwarded-User": "u@corp.test"},
		"frame":  {"Sec-Fetch-Dest": "frame", "X-Forwarded-User": "u@corp.test"},
		// No ForwardAuth identity: public project / bypass path.
		"unprotected": {"Sec-Fetch-Dest": "document"},
		// Clients without Fetch Metadata cannot be told apart from an iframe.
		"no fetch metadata": {"Accept": "text/html", "X-Forwarded-User": "u@corp.test"},
	}
	for name, hdr := range cases {
		t.Run(name, func(t *testing.T) {
			body := spliceFor(t, hdr)
			if strings.Contains(body, "data-muvee-account") {
				t.Fatalf("%s: marked, want plain tag:\n%s", name, body)
			}
			if !strings.Contains(body, plainTag) {
				t.Fatalf("%s: SDK tag missing:\n%s", name, body)
			}
		})
	}
}

// TestConfigHasNoBadgeKey: io runs an older plugin build mounted from the host
// while muvee-server auto-upgrades; a config key the old build does not know
// could break the router there. The badge must not add one.
func TestConfigHasNoBadgeKey(t *testing.T) {
	var keys []string
	ct := reflect.TypeOf(Config{})
	for i := 0; i < ct.NumField(); i++ {
		keys = append(keys, ct.Field(i).Tag.Get("json"))
	}
	want := []string{"scriptPath,omitempty", "stripCookies,omitempty", "stripOnly,omitempty"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("plugin config keys = %v, want exactly %v (no new keys: io drift)", keys, want)
	}
}

func TestSDKIncludesAccountBadge(t *testing.T) {
	rr := httptest.NewRecorder()
	newPlugin(t, &fakeNext{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/_embed-bridge.js", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"__muveeAccountBadge",
		"data-muvee-account",       // only runs on a marked tag
		"/_oauth/account-ui",       // authservice decides
		"/_oauth/me",               // the only thing the badge links to
		"window.parent !== window", // never inside an iframe
		"attachShadow",
		"data-html2canvas-ignore",
		"localStorage",
		"sessionStorage",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SDK missing %q", want)
		}
	}
	badge := sdkAccountBadgeScript
	// Styles go through CSSOM only: a style attribute or <style> element is
	// what a tenant's style-src CSP would block. innerHTML is avoided
	// altogether.
	for _, bad := range []string{"setAttribute('style'", `setAttribute("style"`, "<style", "innerHTML", ".cssText"} {
		if strings.Contains(badge, bad) {
			t.Errorf("badge script uses %q", bad)
		}
	}
}
