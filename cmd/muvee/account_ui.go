package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// accountUIPath answers the embed-bridge SDK's account badge
// (BACKLOG-20261121-001): should this page show the small link to /_oauth/me?
const accountUIPath = "/_oauth/account-ui"

// handleAccountUI decides the account badge for a subsite page. The badge is
// shown when the project's account_badge switch is on and the request carries
// a live session of this project that is allowed in — the same answer the
// page's own ForwardAuth check just gave, read through the same internal
// access check, which also returns the switch (no extra round trip).
//
// It runs the revocation check through that access check, as handleVerify
// does, so it reads the credential with resolveRequestClaims directly
// (TestResolveRequestClaimsOnlyInVerify allows it).
//
// The reply is only {"show": bool}: no identity (the badge text is fixed),
// never cached, never sets or clears a cookie, and every doubt — no session,
// another project's session, an unbound tunnel, a password / phone session
// (no users row to read the switch through), a cross-site caller, muvee-server
// unreachable — answers false with 200: the badge is optional chrome and the
// SDK just does not render it.
func handleAccountUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	show := accountBadgeShown(r)
	_ = json.NewEncoder(w).Encode(map[string]bool{"show": show})
}

func accountBadgeShown(r *http.Request) bool {
	// Fetch Metadata, when present, must say the page asking is the
	// subsite's own. Absent (older clients) is left to the session cookie,
	// which is host-only (__Host-) anyway.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	aud := routeAudience(r)
	projectID, ok := strings.CutPrefix(aud, "project:")
	if !ok || projectID == "" {
		return false
	}
	claims, _, err := resolveRequestClaims(r, aud)
	if err != nil || !userCredential(claims) {
		return false
	}
	check, err := checkProjectAccess(r.Context(), projectID, claims)
	if err != nil {
		log.Printf("authservice: account-ui access check (project=%s %s): %v", projectID, claimsSubjectForLog(claims), err)
		return false
	}
	return check.Allowed && !check.SessionRevoked && check.AccountBadge
}
