package main

// BACKLOG-20261121-001 P3: /_oauth/account-ui answers the embed-bridge SDK's
// "show the account badge here?" — the project's account_badge switch, for a
// live session of this host. It is the only place the switch is decided (the
// plugin has no config for it), it never caches, and every doubt answers
// {"show": false} rather than an error: the badge is optional chrome.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type accountUIStub struct {
	mu    sync.Mutex
	calls []string // path?query
	reply string   // access/check body
	code  int
}

func (s *accountUIStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.URL.Path+"?"+r.URL.RawQuery)
	reply, code := s.reply, s.code
	s.mu.Unlock()
	if code != 0 && code != http.StatusOK {
		http.Error(w, "boom", code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == pathAccessCheck {
		_, _ = w.Write([]byte(reply))
		return
	}
	_, _ = w.Write([]byte(`{"revoked":false}`))
}

func (s *accountUIStub) callPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		p, _, _ := strings.Cut(c, "?")
		out = append(out, p)
	}
	return out
}

func accountUIEnv(t *testing.T, reply string) *accountUIStub {
	t.Helper()
	epEnv(t)
	st := &accountUIStub{reply: reply}
	srv := httptest.NewServer(st)
	t.Cleanup(srv.Close)
	prev := muveeServerURL
	muveeServerURL = srv.URL
	t.Cleanup(func() { muveeServerURL = prev })
	return st
}

func accountUI(t *testing.T, host, aud string, mod func(*http.Request), cookies ...*http.Cookie) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	r := epReq("GET", host, "/_oauth/account-ui", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if aud != "" {
		r.Header.Set(routeAudienceHeader, aud)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	handleAccountUI(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if len(body) != 1 {
		t.Errorf("body = %v, want only {show} (no identity in this reply)", body)
	}
	show, _ := body["show"].(bool)
	return show, w
}

func TestAccountUI_FollowsProjectSwitch(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply string
		want  bool
	}{
		{"on", `{"allowed":true,"mode":"public","account_badge":true}`, true},
		{"off", `{"allowed":true,"mode":"public","account_badge":false}`, false},
		// A muvee-server that predates the switch.
		{"old server", `{"allowed":true,"mode":"public"}`, false},
		{"not allowed", `{"allowed":false,"mode":"private","account_badge":true}`, false},
		{"revoked", `{"allowed":true,"mode":"public","session_revoked":true,"account_badge":true}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := accountUIEnv(t, c.reply)
			show, _ := accountUI(t, epApp, epAud, nil, subsiteCookie(t, epIdentity, epAud, "sso-ui"))
			if show != c.want {
				t.Fatalf("show = %v, want %v", show, c.want)
			}
			// One internal call: the access check, which also answers
			// revocation — no separate session-revoked round trip.
			if paths := st.callPaths(); len(paths) != 1 || paths[0] != pathAccessCheck {
				t.Fatalf("upstream calls = %v, want exactly [%s]", paths, pathAccessCheck)
			}
			q, _ := url.ParseQuery(strings.SplitN(st.calls[0], "?", 2)[1])
			if q.Get("project_id") != epProjectID || q.Get("email") != epIdentity.Email || q.Get("session_iat") == "" || q.Get("sids") == "" {
				t.Errorf("access check query = %v", q)
			}
		})
	}
}

func TestAccountUI_NoShowWithoutLiveProjectSession(t *testing.T) {
	on := `{"allowed":true,"mode":"public","account_badge":true}`
	pwTok, err := signProjectSession("demo", "Demo", "", "password", epProjectID)
	if err != nil {
		t.Fatal(err)
	}
	expired := func(t *testing.T) *http.Cookie {
		c := identityClaims(epIdentity)
		c.Kind, c.SID = fwdKindSession, "x"
		tok, err := signFwdClaims(c, -time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok}
	}
	cases := []struct {
		name    string
		host    string
		aud     string
		mod     func(*http.Request)
		cookies func(t *testing.T) []*http.Cookie
	}{
		{"no cookie", epApp, epAud, nil, func(*testing.T) []*http.Cookie { return nil }},
		{"expired", epApp, epAud, nil, func(t *testing.T) []*http.Cookie { return []*http.Cookie{expired(t)} }},
		{"other project's session", epApp, epAud, nil, func(t *testing.T) []*http.Cookie {
			return []*http.Cookie{subsiteCookie(t, epIdentity, "project:ep-proj-2", "sso-x")}
		}},
		// An unbound tunnel host has no project to carry a switch.
		{"tunnel host", epTunnel, "host:" + epTunnel, nil, func(t *testing.T) []*http.Cookie {
			return []*http.Cookie{subsiteCookie(t, epIdentity, "host:"+epTunnel, "sso-t")}
		}},
		{"no audience", epApp, "", nil, func(t *testing.T) []*http.Cookie {
			return []*http.Cookie{subsiteCookie(t, epIdentity, epAud, "sso-y")}
		}},
		// The central sign-in host is never a subsite.
		{"central host", epCentral, epAud, nil, func(t *testing.T) []*http.Cookie {
			return []*http.Cookie{subsiteCookie(t, epIdentity, epAud, "sso-z")}
		}},
		// Password / phone sessions have no users row to read the switch
		// through; they keep /_oauth/me but get no badge.
		{"password session", epApp, epAud, nil, func(*testing.T) []*http.Cookie {
			return []*http.Cookie{{Name: fwdCookieName(fwdSessionCookieBase), Value: pwTok}}
		}},
		// Only the subsite's own pages ask.
		{"cross-site fetch", epApp, epAud, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, func(t *testing.T) []*http.Cookie {
			return []*http.Cookie{subsiteCookie(t, epIdentity, epAud, "sso-w")}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := accountUIEnv(t, on)
			show, w := accountUI(t, c.host, c.aud, c.mod, c.cookies(t)...)
			if show {
				t.Fatalf("show = true, want false")
			}
			if paths := st.callPaths(); len(paths) != 0 {
				t.Errorf("upstream calls = %v, want none", paths)
			}
			if len(w.Result().Cookies()) != 0 {
				t.Errorf("account-ui must not touch cookies: %v", w.Result().Cookies())
			}
		})
	}
}

func TestAccountUI_UpstreamFailureIsQuiet(t *testing.T) {
	st := accountUIEnv(t, "")
	st.code = http.StatusInternalServerError
	if show, _ := accountUI(t, epApp, epAud, nil, subsiteCookie(t, epIdentity, epAud, "sso-f")); show {
		t.Fatal("show = true on upstream failure")
	}
}
