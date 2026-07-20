package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoveychen/muvee/internal/auth"
)

// loginTokenEntry holds the lifecycle of a single SDK-initiated sign-in. The
// SDK polls via login_token; once the OAuth callback completes successfully,
// handleOAuthCallback flips Status to "success" and populates the identity
// fields. The poll endpoint then returns the user payload and deletes the
// entry (single-use semantics).
type loginTokenEntry struct {
	Provider     string
	ProjectID    string
	Status       string // "pending" | "success" | "expired" | "error"
	Error        string // populated when Status == "error"
	Email        string
	Name         string
	AvatarURL    string
	ProviderName string
	ExpiresAt    time.Time
}

var loginTokens sync.Map // map[string]*loginTokenEntry

const (
	loginTokenExpiry  = 10 * time.Minute
	loginTokenPollSec = 2
)

func generateLoginToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// applySDKCORS extends the cross-origin headers applied to /_oauth/userinfo so
// that the SDK's POST + Content-Type: application/json requests pass the
// browser preflight check. Same origin policy (BASE_DOMAIN subtree) as
// /_oauth/userinfo — see applyUserInfoCORS.
func applySDKCORS(w http.ResponseWriter, r *http.Request) {
	applyUserInfoCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// handleOAuthOptionsPreflight short-circuits CORS preflight for SDK endpoints.
// Reused by every /_oauth/login-token* route.
func handleOAuthOptionsPreflight(w http.ResponseWriter, r *http.Request) {
	applySDKCORS(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// handleLoginTokenCreate is the SDK's entry point. It validates the requested
// provider against the per-project enabled set, mints a login_token, and
// returns the OAuth authorization URL the SDK should ask the host environment
// to open (window.open, Tauri shell, RN Linking, ...). The login_token itself
// is the polling handle — it is never embedded in the OAuth URL, so leaking
// the URL does not let a third party hijack the resulting identity.
func handleLoginTokenCreate(w http.ResponseWriter, r *http.Request) {
	applySDKCORS(w, r)

	var body struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	providerName := strings.ToLower(strings.TrimSpace(body.Provider))
	if providerName == "" {
		http.Error(w, "provider required", http.StatusBadRequest)
		return
	}
	p, ok := providers()[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	host := inboundHost(r)
	if host == "" {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}
	cfg, err := fetchProjectAuthConfigByHost(r.Context(), host)
	if err != nil {
		log.Printf("authservice: project-by-host(%s): %v", host, err)
		http.Error(w, "project lookup failed", http.StatusBadGateway)
		return
	}
	if cfg == nil {
		http.Error(w, "no project for host", http.StatusNotFound)
		return
	}

	enabled := false
	for _, allowed := range projectEnabledFwdProviders(cfg.EnabledProviders) {
		if allowed.Name() == providerName {
			enabled = true
			break
		}
	}
	if !enabled {
		http.Error(w, "provider not enabled for this project", http.StatusForbidden)
		return
	}

	token := generateLoginToken()
	loginTokens.Store(token, &loginTokenEntry{
		Provider:  providerName,
		ProjectID: cfg.ProjectID,
		Status:    "pending",
		ExpiresAt: time.Now().Add(loginTokenExpiry),
	})
	// Lazy GC — match the device flow's pattern (authservice.go:568-572) so
	// abandoned tokens don't pile up forever in the sync.Map.
	go func() {
		time.Sleep(loginTokenExpiry + time.Minute)
		loginTokens.Delete(token)
	}()

	state, err := signState(stateClaims{Mode: "login-token", LoginToken: token})
	if err != nil {
		log.Printf("authservice: signState: %v", err)
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"login_token":   token,
		"oauth_url":     p.AuthCodeURL(state, oauthRedirectForHost(host, providerName)),
		"expires_in":    int(loginTokenExpiry.Seconds()),
		"poll_interval": loginTokenPollSec,
	})
}

// handleLoginTokenPoll is polled by the SDK every poll_interval seconds. A
// successful poll consumes the login_token (single-use) and returns the
// authenticated user. Errors and expiries also consume the token so the SDK
// stops polling.
func handleLoginTokenPoll(w http.ResponseWriter, r *http.Request) {
	applySDKCORS(w, r)

	var body struct {
		LoginToken string `json:"login_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(body.LoginToken)
	if token == "" {
		http.Error(w, "login_token required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	val, ok := loginTokens.Load(token)
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "expired"})
		return
	}
	entry := val.(*loginTokenEntry)
	if time.Now().After(entry.ExpiresAt) {
		loginTokens.Delete(token)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "expired"})
		return
	}

	switch entry.Status {
	case "success":
		loginTokens.Delete(token)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"user": map[string]string{
				"email":      entry.Email,
				"name":       entry.Name,
				"avatar_url": entry.AvatarURL,
				"provider":   entry.ProviderName,
			},
		})
	case "error":
		loginTokens.Delete(token)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  entry.Error,
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
	}
}

// markLoginTokenError tags an in-flight login-token as failed so the SDK's
// next poll bubbles the error to the caller and stops the loop. Best-effort:
// if the token has already been GC'd, the SDK will see "expired" instead,
// which is also a terminal status.
func markLoginTokenError(token, msg string) {
	if val, ok := loginTokens.Load(token); ok {
		e := val.(*loginTokenEntry)
		e.Status = "error"
		e.Error = msg
	}
}

// loginCompletePage is shown to the user in whichever window/tab ran the
// OAuth round-trip. The page closes itself after a beat so SDK consumers that
// opened a popup see it auto-disappear; consumers that opened a new tab still
// see the text in case window.close() is blocked.
const loginCompletePage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Sign-in complete</title>
<style>
  body{font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f5f5f5}
  .card{background:#fff;border-radius:12px;padding:2.5rem 3rem;box-shadow:0 4px 24px rgba(0,0,0,.08);text-align:center}
  h2{margin:0 0 .5rem;color:#111}
  p{color:#666;font-size:.95rem;margin:0}
</style>
</head>
<body>
<div class="card">
<h2>&#10003; Sign-in complete</h2>
<p>You can close this window and return to the app.</p>
</div>
<script>setTimeout(function(){try{window.close();}catch(e){}},1000);</script>
</body>
</html>`

// handleLoginTokenCallback is the OAuth callback branch taken when state
// decodes as a login-token flow. Mirrors the legacy handleOAuthCallback path
// (exchange code → identity → upsert upstream) but, instead of writing a
// browser session and 302-ing, it flips the matching login-token entry to
// "success" and serves a static completion page. The polling SDK picks the
// result up on its next /_oauth/login-token/poll call.
func handleLoginTokenCallback(w http.ResponseWriter, r *http.Request, p auth.Provider, providerName, loginToken string) {
	val, ok := loginTokens.Load(loginToken)
	if !ok {
		http.Error(w, "login session expired", http.StatusGone)
		return
	}
	entry := val.(*loginTokenEntry)
	if time.Now().After(entry.ExpiresAt) {
		loginTokens.Delete(loginToken)
		http.Error(w, "login session expired", http.StatusGone)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		// DingTalk uses authCode, mirroring authservice.go:445-447.
		code = r.URL.Query().Get("authCode")
	}
	if code == "" {
		markLoginTokenError(loginToken, "missing authorization code")
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	email, name, avatarURL, err := p.UserInfo(ctx, code, oauthRedirectForHost(inboundHost(r), providerName))
	if err != nil {
		log.Printf("authservice: UserInfo (login-token, %s): %v", providerName, err)
		markLoginTokenError(loginToken, "authentication failed")
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	if err := upsertUserUpstream(ctx, providerName, email, name, avatarURL); err != nil {
		log.Printf("authservice: upstream identity upsert (login-token, %s, %s): %v", providerName, email, err)
		markLoginTokenError(loginToken, "identity sync failed")
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	entry.Email = email
	entry.Name = name
	entry.AvatarURL = avatarURL
	entry.ProviderName = providerName
	entry.Status = "success"

	// Also issue the standard forward-auth session cookie. If the OAuth round
	// trip happened in the same browser as the SDK (the common web case), the
	// SPA gets the cookie automatically and onAuthChange listeners on other
	// tabs of the same project subdomain will fire too.
	if signed, err := signForwardJWT(email, name, avatarURL, providerName); err == nil {
		http.SetCookie(w, &http.Cookie{
			Name:     "muvee_fwd_session",
			Value:    signed,
			MaxAge:   7 * 24 * 3600,
			HttpOnly: true,
			Path:     "/",
			Domain:   cookieDomainForRequest(r),
			SameSite: http.SameSiteLaxMode,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, loginCompletePage)
}
