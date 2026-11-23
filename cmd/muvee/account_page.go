package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/hoveychen/muvee/internal/domains"
)

// accountPagePath is the per-host account page (BACKLOG-20261121-001 X1).
// Tenants link to it as their sign-out entry; the console's "sites you
// signed in to" list links to it on every subsite.
const accountPagePath = "/_oauth/me"

// consoleAccountPath is the console page holding the "subsite sign-ins" card
// (recent subsites + sign out of all subsites).
const consoleAccountPath = "/settings/profile"

// accountView is the account-page variant of loginPageData.
type accountView struct {
	SignedIn    bool
	Central     bool // the base domain's central sign-in host (shared SSO)
	Unavailable bool // muvee-server could not answer the revocation check
	Label       string
	Name        string
	AvatarURL   string
	Method      string
	LogoutURL   string
	SwitchURL   string // empty on the central host (it serves no login page)
	LoginURL    string // empty on the central host
	ConsoleURL  string
}

// handleAccountPage — GET /_oauth/me on any muvee-routed host. A subsite shows
// the account its own session belongs to (the same credential
// /_oauth/userinfo resolves, so a session of another site never shows up
// here) with sign-out, switch account and a link to the console. The central
// host shows the base domain's shared sign-in (SSO session) instead: subsite
// sessions and X-Muvee-Aud are never honoured there.
//
// The page is the host's own, never cached, never framed by another origin;
// its links are same-origin so /_oauth/logout signs out directly.
func handleAccountPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	host := inboundHost(r)
	v := &accountView{ConsoleURL: forwardAuthBaseForHost(host) + consoleAccountPath}
	var claims *authClaims
	status := http.StatusOK
	var cfg *projectAuthConfig

	if isCentralHost(r) {
		v.Central = true
		v.LogoutURL = "/_oauth/logout?redirect=" + url.QueryEscape(accountPagePath)
		c, err := currentSSO(w, r, domains.NormalizeHost(host))
		if err != nil {
			log.Printf("authservice: account page SSO check (%s): %v", host, err)
			v.Unavailable, status = true, http.StatusBadGateway
		}
		claims = c
	} else {
		v.LogoutURL = "/_oauth/logout"
		v.SwitchURL = "/_oauth/logout?redirect=" + url.QueryEscape("/_oauth/login?sso=none")
		v.LoginURL = "/_oauth/login"
		c, err := resolveAuthClaims(w, r, routeAudience(r))
		switch {
		case errors.Is(err, errRevocationCheck):
			v.Unavailable, status = true, http.StatusBadGateway
		case err == nil:
			claims = c
		}
		if fetched, err := fetchProjectAuthConfigByHost(r.Context(), host); err == nil {
			cfg = fetched
		}
	}

	m := loginMessages(acceptLang(r))
	if claims != nil {
		v.SignedIn = true
		v.Label = claimsAccountLabel(claims)
		if claims.Name != "" && claims.Name != v.Label {
			v.Name = claims.Name
		}
		v.AvatarURL = claims.AvatarURL
		v.Method = signInMethodLabel(claims.Provider, m)
	}

	data := buildLoginPageData(cfg, nil)
	data.M = m
	data.Account = v
	w.WriteHeader(status)
	_ = loginPageTmpl.Execute(w, data)
}

// signInMethodLabel is the user-facing name of the way a session signed in.
func signInMethodLabel(provider string, m loginMsg) string {
	switch provider {
	case "":
		return ""
	case "password":
		return m.MethodPassword
	case "phone":
		return m.MethodPhone
	}
	if p, ok := providers()[provider]; ok {
		return p.DisplayName()
	}
	return provider
}
