package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hoveychen/muvee/internal/auth"
)

type authClaims struct {
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Provider  string `json:"provider,omitempty"`
	jwt.RegisteredClaims
}

var (
	// fwdProvidersAtomic is the source-of-truth for the active provider set.
	// Replaced atomically by reloadProviders so /_oauth/internal/reload can
	// pick up social-provider config changes without restarting the
	// container. Read via the providers() accessor; never read the pointer
	// field directly.
	fwdProvidersAtomic atomic.Pointer[map[string]auth.Provider]
	jwtSecret          []byte
	adminEmails        map[string]struct{}
	cookieDomain       string
	forwardAuthBase    string // e.g. "https://example.com"
	muveeServerURL     string // internal URL for /api/internal/access/check
	internalKey        string // sha256(JWT_SECRET) — shared with muvee-server
	internalClient     = &http.Client{Timeout: 5 * time.Second}
)

// providers returns a snapshot of the current provider map. Safe for
// concurrent reads while reloadProviders is swapping the pointer.
func providers() map[string]auth.Provider {
	if p := fwdProvidersAtomic.Load(); p != nil {
		return *p
	}
	return nil
}

// reloadProviders rebuilds the provider set: platform providers from env
// vars, then social providers from muvee-server's
// /api/internal/oauth/social-providers. Called once at startup and again on
// every /_oauth/internal/reload (POSTed by muvee-server after PUT
// /api/admin/settings touches any social_* key). Any error keeps the
// previous provider set in place so a malformed setting cannot lock all
// users out.
func reloadProviders(ctx context.Context) error {
	platform, err := auth.NewForwardAuthProviders(forwardAuthBase)
	if err != nil {
		return fmt.Errorf("platform providers: %w", err)
	}
	social, err := fetchSocialConfigs(ctx)
	if err != nil {
		log.Printf("authservice: fetch social configs failed (continuing with platform only): %v", err)
		social = auth.SocialConfigs{}
	}
	socialProviders, err := auth.BuildSocialProviders(forwardAuthBase, social)
	if err != nil {
		return fmt.Errorf("build social providers: %w", err)
	}
	merged := make(map[string]auth.Provider, len(platform)+len(socialProviders))
	for k, v := range platform {
		merged[k] = v
	}
	for k, v := range socialProviders {
		merged[k] = v
	}
	fwdProvidersAtomic.Store(&merged)
	return nil
}

// fetchSocialConfigs reads social-OAuth provider configs from muvee-server.
// Returns an empty SocialConfigs (no error) if the server returns 401 to
// guard against half-configured environments where INTERNAL_KEY drifts; the
// log line in reloadProviders surfaces the situation to operators.
func fetchSocialConfigs(ctx context.Context) (auth.SocialConfigs, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		muveeServerURL+"/api/internal/oauth/social-providers", nil)
	if err != nil {
		return auth.SocialConfigs{}, err
	}
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return auth.SocialConfigs{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return auth.SocialConfigs{}, fmt.Errorf("social-providers endpoint returned %d", resp.StatusCode)
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return auth.SocialConfigs{}, err
	}
	return decodeSocialConfigsResponse(body)
}

// decodeSocialConfigsResponse parses the response body of muvee-server's
// /api/internal/oauth/social-providers endpoint. The handler uses jsonOK,
// which encodes the value directly — no `{"data": ...}` wrapper — so this
// just unmarshals straight into SocialConfigs. An earlier version assumed
// the wrapper and unmarshaled body into a `{Data: SocialConfigs}` shape;
// because Go's json.Unmarshal silently ignores unknown top-level fields
// rather than failing, that wrapper unmarshal returned nil error with an
// empty Data even on real payloads — every admin-configured social
// provider got dropped on the floor.
func decodeSocialConfigsResponse(body []byte) (auth.SocialConfigs, error) {
	var cfg auth.SocialConfigs
	if err := json.Unmarshal(body, &cfg); err != nil {
		return auth.SocialConfigs{}, fmt.Errorf("decode social configs: %w", err)
	}
	return cfg, nil
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// handleInternalReload re-fetches social-OAuth configs from muvee-server
// and atomically swaps the provider set. Authenticated with
// X-Muvee-Internal-Key (the same shared secret used for the /verify ←→
// /access/check internal channel).
func handleInternalReload(w http.ResponseWriter, r *http.Request) {
	expected := internalKey
	got := r.Header.Get("X-Muvee-Internal-Key")
	if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := reloadProviders(r.Context()); err != nil {
		log.Printf("authservice: reload failed: %v", err)
		http.Error(w, "reload failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleInternalBaseURL returns the authservice's own FORWARD_AUTH_BASE_URL
// so muvee-server can show admins the exact OAuth callback URL to register
// with each social provider's dashboard (path = /_oauth/<provider>).
// Authenticated with X-Muvee-Internal-Key.
func handleInternalBaseURL(w http.ResponseWriter, r *http.Request) {
	expected := internalKey
	got := r.Header.Get("X-Muvee-Internal-Key")
	if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"forward_auth_base_url": forwardAuthBase,
	})
}

func runAuthservice() {
	baseURL := os.Getenv("FORWARD_AUTH_BASE_URL")
	if baseURL == "" {
		// Backward compat: derive from old FORWARD_AUTH_REDIRECT_URL (strip trailing "/_oauth").
		if old := os.Getenv("FORWARD_AUTH_REDIRECT_URL"); old != "" {
			baseURL = strings.TrimSuffix(strings.TrimRight(old, "/"), "/_oauth")
		}
	}
	if baseURL == "" {
		baseURL = "http://localhost:4181"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	forwardAuthBase = baseURL

	cookieDomain = os.Getenv("BASE_DOMAIN")
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		log.Fatal("JWT_SECRET environment variable is required (was empty)")
	}
	if len(secret) < 32 {
		log.Printf("Warning: JWT_SECRET is %d bytes (strongly recommended >= 32)", len(secret))
	}
	jwtSecret = []byte(secret)
	h := sha256.Sum256([]byte(secret))
	internalKey = hex.EncodeToString(h[:])

	muveeServerURL = strings.TrimRight(os.Getenv("MUVEE_SERVER_URL"), "/")
	if muveeServerURL == "" {
		muveeServerURL = "http://muvee-server:8080"
	}

	adminEmails = make(map[string]struct{})
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			adminEmails[e] = struct{}{}
		}
	}

	if err := reloadProviders(context.Background()); err != nil {
		log.Fatalf("init auth providers: %v", err)
	}
	if len(providers()) == 0 {
		log.Fatal("no auth providers configured; set at least one of GOOGLE_CLIENT_ID, FEISHU_APP_ID, WECOM_CORP_ID, DINGTALK_CLIENT_ID, or enable a social provider via admin/settings")
	}

	r := chi.NewRouter()
	r.Get("/verify", handleVerify)
	r.Get("/verify-admin", handleVerifyAdmin)
	r.Get("/_oauth/userinfo", handleUserInfo)
	r.Get("/_oauth/providers", handleProviders)
	r.Post("/_oauth/login-token", handleLoginTokenCreate)
	r.Options("/_oauth/login-token", handleOAuthOptionsPreflight)
	r.Post("/_oauth/login-token/poll", handleLoginTokenPoll)
	r.Options("/_oauth/login-token/poll", handleOAuthOptionsPreflight)
	r.Get("/_oauth/logout", handleFwdLogout)
	r.Get("/_oauth/login", handleLoginPage)
	r.Get("/_oauth/request-access", handleRequestAccessPage)
	r.Post("/_oauth/request-access", handleRequestAccessSubmit)
	// Internal reload endpoint (muvee-server posts here after PUT /admin/settings
	// touches a social_* key, see settings.go).
	r.Post("/_oauth/internal/reload", handleInternalReload)
	// Internal endpoint that returns FORWARD_AUTH_BASE_URL so muvee-server
	// can display the canonical OAuth callback URL to admins.
	r.Get("/_oauth/internal/base-url", handleInternalBaseURL)
	// {provider} catch-all must come after the more specific /_oauth/* routes
	// above; chi matches in registration order for static segments.
	r.Get("/_oauth/{provider}", handleOAuthCallback)

	// Device Flow for CLI / headless access
	r.Post("/_oauth/device/code", handleDeviceCode)
	r.Get("/_oauth/device/activate", handleDeviceActivate)
	r.Post("/_oauth/device/token", handleDeviceToken)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4181"
	}
	var names []string
	for n := range providers() {
		names = append(names, n)
	}
	log.Printf("muvee authservice listening on :%s (providers: %s)", port, strings.Join(names, ", "))
	log.Fatal(http.ListenAndServe(":"+port, r))
}

// resolveAuthClaims extracts auth claims from the request, trying the session
// cookie first, then falling back to an Authorization: Bearer JWT header.
func resolveAuthClaims(r *http.Request) (*authClaims, error) {
	if cookie, err := r.Cookie("muvee_fwd_session"); err == nil {
		if claims, err := parseForwardJWT(cookie.Value); err == nil {
			return claims, nil
		}
	}
	if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
		token := strings.TrimPrefix(bearer, "Bearer ")
		return parseForwardJWT(token)
	}
	return nil, fmt.Errorf("no credentials")
}

// handleVerify is the Traefik ForwardAuth endpoint for regular users.
func handleVerify(w http.ResponseWriter, r *http.Request) {
	// Look at the user-visible URL (X-Forwarded-Uri) for ?invite_token=... —
	// r.URL only carries the Traefik forward-auth parameters (project_id /
	// domains), not the browser's query string.
	inviteToken := extractInviteTokenFromForwardedURI(r)

	claims, err := resolveAuthClaims(r)
	if err != nil {
		// Unauthenticated visitor with an invite token: stash the token in a
		// short-lived cookie so handleOAuthCallback can consume it once OAuth
		// completes (the post-OAuth redirect back to the original URL would
		// otherwise lose the query string visibility to the callback handler).
		if inviteToken != "" {
			setInviteTokenCookie(w, inviteToken)
		}
		redirectToLogin(w, r)
		return
	}

	// Already-authenticated visitor with an invite token: consume the link
	// in-place against muvee-server (records use + adds to project_access_users)
	// so the access check below admits them on this same request. Best-effort:
	// log + ignore errors so a stale / invalid token never blocks a valid
	// session.
	if inviteToken != "" {
		if err := consumeInviteUpstream(r.Context(), claims.Provider, claims.Email, claims.Name, claims.AvatarURL, inviteToken); err != nil {
			log.Printf("authservice: consume invite (authed, email=%s): %v", claims.Email, err)
		}
		clearInviteTokenCookie(w)
	}

	if allowedDomains := r.URL.Query().Get("domains"); allowedDomains != "" {
		if !emailMatchesDomains(claims.Email, allowedDomains) {
			http.Error(w, "access denied: email domain not permitted", http.StatusForbidden)
			return
		}
	}
	if projectID := r.URL.Query().Get("project_id"); projectID != "" {
		check, err := checkProjectAccess(r.Context(), projectID, claims.Email)
		if err != nil {
			log.Printf("authservice: access check (project=%s email=%s): %v", projectID, claims.Email, err)
			http.Error(w, "access check failed", http.StatusBadGateway)
			return
		}
		if !check.Allowed {
			// Send the user to the request-access page on the same subdomain
			// they tried to reach (e.g. https://my-project.example.com/_oauth/request-access?project=X).
			// Keeping the URL on the project's own host means downstream
			// users never get a glimpse of the platform main domain.
			if redirect := requestAccessRedirectURL(r, projectID); redirect != "" {
				http.Redirect(w, r, redirect, http.StatusFound)
				return
			}
			http.Error(w, "access denied: not a member of this project", http.StatusForbidden)
			return
		}
	}
	setUserHeaders(w, claims)
	w.WriteHeader(http.StatusOK)
}

// upsertUserUpstream syncs an OAuth-verified identity into muvee-server's
// users table via the X-Muvee-Internal-Key-gated identity-upsert endpoint.
// Called from handleOAuthCallback so that users authenticating only through
// ForwardAuth subdomains (never through the apex Portal) still appear in the
// users table — required by IsProjectAccessAllowedByEmail.
//
// The endpoint is identity-only: no domain check, no invite gate, no
// platform_members row. Subdomain users have their own per-project access
// control (project_access_users + projects.auth_allowed_domains) so the
// platform's invite list and ALLOWED_DOMAINS do not apply.
func upsertUserUpstream(ctx context.Context, providerName, email, name, avatarURL string) error {
	body, err := json.Marshal(map[string]string{
		"email":      email,
		"name":       name,
		"avatar_url": avatarURL,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		muveeServerURL+"/api/internal/auth/identity-upsert",
		strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("upstream identity upsert returned %d", resp.StatusCode)
}

// inviteTokenCookieName is the short-lived cookie that carries the value of
// a `?invite_token=...` query parameter across the OAuth handoff. Set by
// handleVerify when an unauthenticated visitor first hits the project
// subdomain with the token, consumed by handleOAuthCallback once the user
// completes OAuth — at which point we know who they are and can call
// /api/internal/auth/upsert with the token to add them to
// project_access_users.
const inviteTokenCookieName = "muvee_invite_token"

// extractInviteTokenFromForwardedURI returns the invite_token query parameter
// from the user-visible URL. Behind Traefik forward-auth, r.URL.Query() only
// carries the forward-auth middleware's own params (project_id, domains) —
// the browser's query string is exposed via X-Forwarded-Uri.
func extractInviteTokenFromForwardedURI(r *http.Request) string {
	uri := r.Header.Get("X-Forwarded-Uri")
	if uri == "" {
		return ""
	}
	q := strings.IndexByte(uri, '?')
	if q < 0 {
		return ""
	}
	vals, err := url.ParseQuery(uri[q+1:])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(vals.Get("invite_token"))
}

func setInviteTokenCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: inviteTokenCookieName, Value: token,
		MaxAge: 300, HttpOnly: true, Path: "/", Domain: cookieDomain,
		SameSite: http.SameSiteLaxMode, Secure: true,
	})
}

func clearInviteTokenCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: inviteTokenCookieName, Value: "", MaxAge: -1,
		HttpOnly: true, Path: "/", Domain: cookieDomain,
	})
}

// consumeInviteUpstream calls muvee-server's /api/internal/auth/upsert with
// the invite_token, which threads through EnsurePlatformMember to record the
// link use and add the user to project_access_users (see auth.go). Used in
// two contexts: the authed-already path in handleVerify, and the post-OAuth
// callback path when the cookie was set by an earlier verify hop.
func consumeInviteUpstream(ctx context.Context, providerName, email, name, avatarURL, inviteToken string) error {
	body, err := json.Marshal(map[string]string{
		"email":        email,
		"name":         name,
		"avatar_url":   avatarURL,
		"provider":     providerName,
		"invite_token": inviteToken,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		muveeServerURL+"/api/internal/auth/upsert",
		strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("upstream auth/upsert returned %d", resp.StatusCode)
}

// accessCheckResult is the structured result from muvee-server's
// /api/internal/access/check endpoint. On deny, authservice constructs the
// request-access redirect itself from X-Forwarded-Host (see
// requestAccessRedirectURL) — the server doesn't know the subdomain.
type accessCheckResult struct {
	Allowed bool   `json:"allowed"`
	Mode    string `json:"mode"`
}

// requestAccessRedirectURL returns the absolute URL of the request-access page
// on the same subdomain the user tried to reach. ForwardAuth runs on the
// authservice but the user's browser sees the project subdomain; we recover
// that from the Traefik X-Forwarded-* headers and bounce them to a path on
// the same host. Returns "" if the headers are missing (the caller falls back
// to a 403 in that case).
func requestAccessRedirectURL(r *http.Request, projectID string) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		return ""
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "https"
	}
	return proto + "://" + host + "/_oauth/request-access?project=" + url.QueryEscape(projectID)
}

// checkProjectAccess asks muvee-server whether the given email is permitted to
// reach the project's downstream service. Public projects always return true;
// private projects consult the per-project allow-list. Errors are propagated to
// the caller so the proxy can fail closed (502) rather than silently allow.
func checkProjectAccess(ctx context.Context, projectID, email string) (accessCheckResult, error) {
	q := url.Values{}
	q.Set("project_id", projectID)
	q.Set("email", email)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		muveeServerURL+"/api/internal/access/check?"+q.Encode(), nil)
	if err != nil {
		return accessCheckResult{}, err
	}
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return accessCheckResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return accessCheckResult{}, fmt.Errorf("internal access check returned %d", resp.StatusCode)
	}
	var body accessCheckResult
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return accessCheckResult{}, err
	}
	return body, nil
}

// handleVerifyAdmin is the Traefik ForwardAuth endpoint restricted to admin emails.
func handleVerifyAdmin(w http.ResponseWriter, r *http.Request) {
	claims, err := resolveAuthClaims(r)
	if err != nil {
		redirectToLogin(w, r)
		return
	}
	if _, ok := adminEmails[claims.Email]; !ok {
		http.Error(w, "access denied: admin only", http.StatusForbidden)
		return
	}
	setUserHeaders(w, claims)
	w.WriteHeader(http.StatusOK)
}

// setUserHeaders writes user identity headers for Traefik to forward downstream.
func setUserHeaders(w http.ResponseWriter, claims *authClaims) {
	w.Header().Set("X-Forwarded-User", claims.Email)
	if claims.Name != "" {
		w.Header().Set("X-Forwarded-User-Name", claims.Name)
	}
	if claims.AvatarURL != "" {
		w.Header().Set("X-Forwarded-User-Avatar", claims.AvatarURL)
	}
	if claims.Provider != "" {
		w.Header().Set("X-Forwarded-User-Provider", claims.Provider)
	}
}

// handleUserInfo returns the authenticated user's info as JSON.
func handleUserInfo(w http.ResponseWriter, r *http.Request) {
	applyUserInfoCORS(w, r)
	claims, err := resolveAuthClaims(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"email":      claims.Email,
		"name":       claims.Name,
		"avatar_url": claims.AvatarURL,
		"provider":   claims.Provider,
	})
}

// applyUserInfoCORS lets SPAs on any project subdomain of BASE_DOMAIN fetch
// /_oauth/userinfo cross-origin with credentials. Origins outside the BASE_DOMAIN
// tree are rejected by simply not echoing the Origin header back.
func applyUserInfoCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || cookieDomain == "" {
		return
	}
	if !originMatchesBaseDomain(origin, cookieDomain) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Vary", "Origin")
}

// originMatchesBaseDomain reports whether origin is https://base or
// https://*.base (scheme is not constrained; a misconfigured plaintext
// deployment would still be accepted, matching how cookies already flow).
func originMatchesBaseDomain(origin, base string) bool {
	// Origin format: scheme://host[:port]
	i := strings.Index(origin, "://")
	if i < 0 {
		return false
	}
	host := origin[i+3:]
	if j := strings.IndexByte(host, '/'); j >= 0 {
		host = host[:j]
	}
	if k := strings.IndexByte(host, ':'); k >= 0 {
		host = host[:k]
	}
	return host == base || strings.HasSuffix(host, "."+base)
}

// handleFwdLogout clears the forward-auth session cookie and redirects.
func handleFwdLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "muvee_fwd_session", Value: "", MaxAge: -1,
		Path: "/", Domain: cookieDomain, HttpOnly: true,
	})
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

// handleLoginPage either auto-redirects (single provider) or shows a
// provider-selection page (multiple providers).  When ?provider=X is present
// it kicks off the OAuth flow for that specific provider. The selection
// page is rendered with the project's branding (or platform / built-in
// fallbacks) so downstream end-users see a coherent visual instead of the
// generic indigo card the page used to show.
func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	providerName := r.URL.Query().Get("provider")
	// Fetch the project's auth config (providers + branding) for the
	// inbound host. cfg is nil for hosts that don't map to a project
	// (apex / platform), in which case we fall back to the global
	// provider set and built-in branding defaults.
	cfg, _ := fetchProjectAuthConfigByHost(r.Context(), inboundHost(r))
	allowed := allowedProvidersFromConfig(cfg)
	if providerName != "" {
		p, ok := allowed[providerName]
		if !ok {
			http.Error(w, "unknown or disabled provider", http.StatusBadRequest)
			return
		}
		state := fmt.Sprintf("%d", time.Now().UnixNano())
		http.SetCookie(w, &http.Cookie{
			Name: "fwd_oauth_state", Value: state,
			MaxAge: 300, HttpOnly: true, Path: "/", Domain: cookieDomain,
			SameSite: http.SameSiteLaxMode, Secure: true,
		})
		http.Redirect(w, r, p.AuthCodeURL(state), http.StatusFound)
		return
	}

	// Auto-redirect when only one provider is configured.
	if len(allowed) == 1 {
		for name := range allowed {
			http.Redirect(w, r, "/_oauth/login?provider="+name, http.StatusFound)
			return
		}
	}

	data := buildLoginPageData(cfg, allowed)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginPageTmpl.Execute(w, data)
}

// allowedProvidersFromConfig returns the provider map honoured by this
// inbound host. When cfg is nil (apex / unmapped host) the global provider
// set is returned unchanged.
func allowedProvidersFromConfig(cfg *projectAuthConfig) map[string]auth.Provider {
	if cfg == nil {
		return providers()
	}
	out := make(map[string]auth.Provider)
	for _, p := range projectEnabledFwdProviders(cfg.EnabledProviders) {
		out[p.Name()] = p
	}
	return out
}

// loginPageData is the template payload for the forward-auth login page.
// All fields are pre-resolved to their final display values (project ->
// platform -> built-in fallback chain) so the template stays declarative.
type loginPageData struct {
	SiteName     string
	LogoURL      string
	FaviconURL   string
	Tagline      string
	Description  string
	FooterText   string
	TrustItems   []string
	PrimaryColor template.CSS
	SidebarBg    template.CSS
	Providers    []loginProviderItem
}

type loginProviderItem struct {
	Name        string
	DisplayName string
	Icon        template.HTML
}

// hexColorRe matches #RGB, #RRGGBB, and #RRGGBBAA. We intentionally do not
// accept rgb()/hsl()/named colours: branding values flow into a <style>
// block and a permissive parser would let an admin smuggle CSS or markup
// into the inlined stylesheet.
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

func safeColor(value, fallback string) template.CSS {
	if hexColorRe.MatchString(value) {
		return template.CSS(value)
	}
	return template.CSS(fallback)
}

// buildLoginPageData resolves the project / platform / built-in fallback
// chain for every visible branding field and produces the template payload.
// Exported variables — colours and the provider list — go through their
// safety wrappers so a malicious branding value cannot escape into raw
// HTML or CSS.
func buildLoginPageData(cfg *projectAuthConfig, allowed map[string]auth.Provider) loginPageData {
	b := projectBranding{}
	if cfg != nil {
		b = cfg.Branding
	}
	// siteName intentionally stays empty when nothing in the fallback chain
	// has a value — the template branches on emptiness (e.g. "Sign in to
	// Acme" vs the bare "Sign in") rather than coercing in a placeholder
	// like "Sign in" that would render as the nonsensical "Sign in to
	// Sign in" when interpolated into the heading.
	siteName := firstNonEmpty(b.SiteName, b.PlatformSiteName)
	if siteName == "" && cfg != nil {
		siteName = cfg.ProjectName
	}
	// logoURL fallback: project branding wins, then platform-wide logo.
	logoURL := firstNonEmpty(b.LogoURL, b.PlatformLogoURL)
	// faviconURL: same fallback chain; empty = template omits the <link>.
	faviconURL := firstNonEmpty(b.FaviconURL, b.PlatformFaviconURL)

	// Default primary uses the same indigo the legacy template used so
	// projects without branding stay visually familiar.
	primary := safeColor(b.PrimaryColor, "#4f46e5")
	// Sidebar defaults to a deep slate so the white logo / tagline pop;
	// projects that only set primary_color get a sidebar tinted with their
	// brand colour for free.
	sidebarFallback := "#0f172a"
	if hexColorRe.MatchString(b.PrimaryColor) {
		sidebarFallback = b.PrimaryColor
	}
	sidebar := safeColor(b.SidebarBg, sidebarFallback)

	// Provider ordering: same canonical order as elsewhere in authservice
	// so the visual stays stable across renders (map iteration in Go is
	// randomised).
	order := []string{"google", "feishu", "wecom", "dingtalk", "discord", "apple", "facebook", "twitter"}
	seen := make(map[string]bool)
	items := make([]loginProviderItem, 0, len(allowed))
	for _, name := range order {
		if p, ok := allowed[name]; ok {
			items = append(items, loginProviderItem{Name: name, DisplayName: p.DisplayName(), Icon: providerIcons[name]})
			seen[name] = true
		}
	}
	for name, p := range allowed {
		if seen[name] {
			continue
		}
		items = append(items, loginProviderItem{Name: name, DisplayName: p.DisplayName(), Icon: providerIcons[name]})
	}

	return loginPageData{
		SiteName:     siteName,
		LogoURL:      logoURL,    // empty = template hides the <img>
		FaviconURL:   faviconURL, // empty = template omits the <link rel="icon">
		Tagline:      b.Tagline,
		Description:  b.Description,
		FooterText:   b.FooterText, // empty = template hides the sidebar footer
		TrustItems:   parseTrustItems(b.TrustText),
		PrimaryColor: primary,
		SidebarBg:    sidebar,
		Providers:    items,
	}
}

// parseTrustItems converts a comma-separated branding_trust_text value into
// the (up to 3) trust-row entries rendered under the provider buttons.
// Empty input returns nil so the template skips the entire trust row —
// project owners "blow it away" by leaving the field blank. Owners who
// want the technical defaults must spell them out explicitly (e.g.
// "Encrypted,SSO,OAuth verified"), and compliance-focused tenants pick
// their own (e.g. "SOC 2,GDPR,HIPAA"). Excess entries past 3 are dropped
// so an over-eager value can't blow out the card layout.
func parseTrustItems(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := make([]string, 0, 3)
	for _, tok := range strings.Split(raw, ",") {
		s := strings.TrimSpace(tok)
		if s == "" {
			continue
		}
		out = append(out, s)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

var loginPageTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .SiteName}}{{.SiteName}} — Sign in{{else}}Sign in{{end}}</title>
{{if .FaviconURL}}<link rel="icon" href="{{.FaviconURL}}">{{end}}
<style>
  *{box-sizing:border-box}
  body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;background:#f8fafc;color:#0f172a;min-height:100vh}
  .page{display:flex;min-height:100vh}
  .sidebar{display:none;flex-direction:column;justify-content:space-between;width:50%;padding:4rem;background:{{.SidebarBg}};color:#fff;position:relative;overflow:hidden}
  .sidebar .tagline{font-size:.8125rem;letter-spacing:.18em;text-transform:uppercase;color:rgba(255,255,255,.7)}
  .sidebar .brand{font-size:5rem;font-weight:700;line-height:1;letter-spacing:-.02em;margin:0}
  .sidebar .brand img{height:80px;object-fit:contain;display:block}
  .sidebar .desc{margin-top:1.5rem;font-size:1rem;line-height:1.7;color:rgba(255,255,255,.85);max-width:24rem;white-space:pre-line}
  .sidebar .footer{display:flex;gap:1rem;font-size:.875rem;color:rgba(255,255,255,.75)}
  .sidebar .footer .sep{color:rgba(255,255,255,.4)}
  .panel{flex:1;display:flex;align-items:center;justify-content:center;padding:2rem}
  .card{width:100%;max-width:22rem;background:#fff;border:1px solid #e2e8f0;border-radius:12px;padding:2rem;box-shadow:0 1px 3px rgba(0,0,0,.04)}
  .mobile-brand{margin-bottom:2.5rem;text-align:center}
  .mobile-brand img{height:48px;object-fit:contain}
  .mobile-brand h1{margin:0;font-size:2.25rem;font-weight:700;color:#0f172a}
  .card h2{margin:0 0 .25rem;font-size:1.25rem;font-weight:600;color:#0f172a}
  .card .sub{margin:0 0 2rem;font-size:.875rem;color:#64748b}
  .btn{display:flex;align-items:center;justify-content:center;gap:10px;width:100%;padding:10px 16px;margin-top:.75rem;border:1px solid #e2e8f0;border-radius:8px;background:#fff;color:#0f172a;font-size:.875rem;font-weight:500;text-decoration:none;transition:border-color .15s,background .15s}
  .btn:first-of-type{margin-top:0}
  .btn:hover{border-color:{{.PrimaryColor}};background:#f8fafc}
  .btn .icon{display:inline-flex;color:#475569}
  .trust{margin-top:1.25rem;display:flex;align-items:center;justify-content:center;gap:1rem;font-size:.75rem;color:#94a3b8}
  .trust span{display:inline-flex;align-items:center;gap:.35rem}
  .trust svg{width:12px;height:12px;stroke:currentColor;fill:none;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}
  @media(min-width:1024px){.sidebar{display:flex}.mobile-brand{display:none}}
</style>
</head>
<body>
<div class="page">
  <aside class="sidebar">
    <div class="tagline">{{if .Tagline}}{{.Tagline}}{{else}}Welcome{{end}}</div>
    <div>
      {{if .LogoURL}}<img class="brand" src="{{.LogoURL}}" alt="{{.SiteName}}">{{else if .SiteName}}<h1 class="brand">{{.SiteName}}</h1>{{else}}<h1 class="brand">Sign in</h1>{{end}}
      {{if .Description}}<p class="desc">{{.Description}}</p>{{end}}
    </div>
    {{if .FooterText}}<div class="footer"><span>{{.FooterText}}</span></div>{{end}}
  </aside>
  <main class="panel">
    <div>
      <div class="mobile-brand">
        {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.SiteName}}">{{else if .SiteName}}<h1>{{.SiteName}}</h1>{{else}}<h1>Sign in</h1>{{end}}
      </div>
      <div class="card">
        <h2>{{if .SiteName}}Sign in to {{.SiteName}}{{else}}Sign in{{end}}</h2>
        <p class="sub">Choose your sign-in method below.</p>
        {{range .Providers}}<a class="btn" href="/_oauth/login?provider={{.Name}}">{{if .Icon}}<span class="icon">{{.Icon}}</span>{{end}}Continue with {{.DisplayName}}</a>{{end}}
      </div>
      {{if .TrustItems}}<div class="trust">
        {{range .TrustItems}}<span><svg viewBox="0 0 24 24"><path d="M20 6 9 17l-5-5"/></svg>{{.}}</span>{{end}}
      </div>{{end}}
    </div>
  </main>
</div>
</body>
</html>`))

// providerIcons holds inline SVG marks for the social providers added in
// migration 038 (oauth_accounts). Platform-side providers omit icons --
// they keep their text-only buttons. The values are template.HTML so Go's
// html/template does not escape the `<svg>` markup.
var providerIcons = map[string]template.HTML{
	"discord":  template.HTML(`<svg viewBox="0 -28.5 256 256" width="18" height="18"><path fill="currentColor" d="M216.856 16.597A208.502 208.502 0 0 0 164.042 0c-2.275 4.113-4.933 9.645-6.766 14.046-19.692-2.961-39.203-2.961-58.533 0-1.832-4.4-4.55-9.933-6.846-14.046a207.809 207.809 0 0 0-52.855 16.638C5.618 67.147-3.443 116.4 1.087 164.956c22.169 16.555 43.653 26.612 64.775 33.193A161.094 161.094 0 0 0 79.735 175.3a136.413 136.413 0 0 1-21.846-10.632 108.636 108.636 0 0 0 5.355-4.237c42.122 19.702 87.89 19.702 129.51 0a131.66 131.66 0 0 0 5.355 4.237 136.07 136.07 0 0 1-21.886 10.653c4.006 8.02 8.638 15.67 13.873 22.848 21.142-6.58 42.646-16.637 64.815-33.213 5.316-56.288-9.08-105.09-38.056-148.36ZM85.474 135.095c-12.645 0-23.015-11.805-23.015-26.18s10.149-26.2 23.015-26.2c12.867 0 23.236 11.805 23.015 26.2.02 14.375-10.148 26.18-23.015 26.18Zm85.051 0c-12.645 0-23.014-11.805-23.014-26.18s10.148-26.2 23.014-26.2c12.867 0 23.236 11.805 23.015 26.2 0 14.375-10.148 26.18-23.015 26.18Z"/></svg>`),
	"apple":    template.HTML(`<svg viewBox="0 0 24 24" width="18" height="18"><path fill="currentColor" d="M17.05 20.28c-.98.95-2.05.8-3.08.35-1.09-.46-2.09-.48-3.24 0-1.44.62-2.2.44-3.06-.35C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.54 4.09l.01-.01zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z"/></svg>`),
	"facebook": template.HTML(`<svg viewBox="0 0 24 24" width="18" height="18"><path fill="currentColor" d="M24 12.073C24 5.405 18.627 0 12 0S0 5.405 0 12.073C0 18.1 4.388 23.094 10.125 24v-8.437H7.078v-3.49h3.047V9.412c0-3.007 1.792-4.668 4.533-4.668 1.313 0 2.686.235 2.686.235v2.953H15.83c-1.491 0-1.956.926-1.956 1.874v2.252h3.328l-.532 3.49h-2.796V24C19.612 23.094 24 18.1 24 12.073z"/></svg>`),
	"twitter":  template.HTML(`<svg viewBox="0 0 24 24" width="18" height="18"><path fill="currentColor" d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>`),
}

// handleOAuthCallback handles the OAuth redirect back from each provider.
func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	providerName := chi.URLParam(r, "provider")
	p, ok := providers()[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	queryState := r.URL.Query().Get("state")

	// SDK-initiated login-token flow uses HMAC-signed structured state instead
	// of the cookie-based anti-CSRF — the SDK and the OAuth window may live in
	// different browsing contexts (separate tab, Tauri shell, RN external
	// browser), so the cookie is unreliable. The HMAC signature alone is
	// sufficient because login-token state binds to a server-side map entry.
	if sc, err := verifyState(queryState); err == nil && sc.Mode == "login-token" {
		handleLoginTokenCallback(w, r, p, providerName, sc.LoginToken)
		return
	}

	stateCookie, err := r.Cookie("fwd_oauth_state")
	if err != nil || stateCookie.Value != queryState {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "fwd_oauth_state", Value: "", MaxAge: -1,
		HttpOnly: true, Path: "/", Domain: cookieDomain,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		code = r.URL.Query().Get("authCode") // DingTalk uses authCode instead of code
	}
	email, name, avatarURL, err := p.UserInfo(ctx, code)
	if err != nil {
		log.Printf("authservice: UserInfo (%s): %v", providerName, err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	// Sync the verified identity into muvee-server's users table BEFORE
	// signing the forward JWT. Without this, users that only ever
	// authenticated through this subdomain ForwardAuth path would not exist
	// in the central `users` table, and IsProjectAccessAllowedByEmail would
	// reject them as "not a member" even on public projects.
	//
	// If an invite_token cookie was set by an earlier verify hop, route to
	// auth/upsert (which runs EnsurePlatformMember → consumes the link +
	// adds project_access_users). Otherwise stick with identity-upsert:
	// domain restrictions and invite-mode gating do not apply to plain
	// subdomain logins (those are platform-side admission rules). Per-project
	// ACL still runs in checkProjectAccess on the subsequent verify.
	inviteToken := ""
	if c, err := r.Cookie(inviteTokenCookieName); err == nil {
		inviteToken = c.Value
	}
	if inviteToken != "" {
		clearInviteTokenCookie(w)
		if err := consumeInviteUpstream(ctx, providerName, email, name, avatarURL, inviteToken); err != nil {
			// Token may be expired / exhausted / revoked. Fall back to
			// identity-upsert so the user still completes login; the access
			// check on the subsequent verify will bounce them to
			// request-access instead of breaking the OAuth round-trip.
			log.Printf("authservice: consume invite (callback, %s, %s): %v; falling back to identity-upsert", providerName, email, err)
			if err := upsertUserUpstream(ctx, providerName, email, name, avatarURL); err != nil {
				log.Printf("authservice: upstream identity upsert (%s, %s): %v", providerName, email, err)
				http.Error(w, "authentication failed", http.StatusInternalServerError)
				return
			}
		}
	} else if err := upsertUserUpstream(ctx, providerName, email, name, avatarURL); err != nil {
		log.Printf("authservice: upstream identity upsert (%s, %s): %v", providerName, email, err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	// Check if this OAuth callback was initiated by the device flow.
	if userCode, ok := devicePending.LoadAndDelete(stateCookie.Value); ok {
		uc := userCode.(string)
		if dc, ok := deviceByUser.Load(uc); ok {
			if val, ok := deviceFlows.Load(dc); ok {
				entry := val.(*deviceFlowEntry)
				entry.Email = email
				entry.Name = name
				entry.AvatarURL = avatarURL
				entry.Provider = providerName
				entry.Completed = true
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"></head>
<body style="font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f5f5f5">
<div style="background:#fff;border-radius:12px;padding:2.5rem 3rem;box-shadow:0 4px 24px rgba(0,0,0,.08);text-align:center">
<h2>&#10003; Device authorized</h2>
<p>You can close this window and return to your terminal.</p>
</div></body></html>`)
		return
	}

	signed, err := signForwardJWT(email, name, avatarURL, providerName)
	if err != nil {
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "muvee_fwd_session", Value: signed,
		MaxAge: 7 * 24 * 3600, HttpOnly: true, Path: "/",
		Domain: cookieDomain, SameSite: http.SameSiteLaxMode,
	})

	redirectCookie, err := r.Cookie("fwd_oauth_redirect")
	redirect := "/"
	if err == nil && redirectCookie.Value != "" {
		redirect = redirectCookie.Value
	}
	http.SetCookie(w, &http.Cookie{
		Name: "fwd_oauth_redirect", Value: "", MaxAge: -1,
		HttpOnly: true, Path: "/", Domain: cookieDomain,
	})
	http.Redirect(w, r, redirect, http.StatusFound)
}

// ---------------------------------------------------------------------------
// Device Flow (RFC 8628-like) for CLI / headless access
// ---------------------------------------------------------------------------

// deviceFlowEntry holds the state of a pending device authorization request.
type deviceFlowEntry struct {
	UserCode  string
	Email     string // populated after OAuth completes
	Name      string
	AvatarURL string
	Provider  string
	ExpiresAt time.Time
	Completed bool
}

var (
	deviceFlows     sync.Map // deviceCode → *deviceFlowEntry
	deviceByUser    sync.Map // userCode  → deviceCode (reverse lookup)
	devicePending   sync.Map // oauthState → deviceCode (link OAuth callback to device flow)
)

const (
	deviceCodeExpiry = 10 * time.Minute
	devicePollInterval = 5 // seconds
	cliTokenExpiry     = 90 * 24 * time.Hour
)

// generateCode returns a cryptographically random alphanumeric string of length n.
func generateCode(n int) string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I/1/O/0 for readability
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[idx.Int64()]
	}
	return string(b)
}

// handleDeviceCode initiates the device authorization flow.
// CLI calls this to get a user code to display and a device code to poll with.
func handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	deviceCode := generateCode(32)
	userCode := generateCode(8)

	entry := &deviceFlowEntry{
		UserCode:  userCode,
		ExpiresAt: time.Now().Add(deviceCodeExpiry),
	}
	deviceFlows.Store(deviceCode, entry)
	deviceByUser.Store(userCode, deviceCode)

	// Lazy cleanup after expiry.
	go func() {
		time.Sleep(deviceCodeExpiry + time.Minute)
		deviceFlows.Delete(deviceCode)
		deviceByUser.Delete(userCode)
	}()

	verificationURI := forwardAuthBase + "/_oauth/device/activate"
	formattedCode := fmt.Sprintf("%s-%s", userCode[:4], userCode[4:])

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_code":               deviceCode,
		"user_code":                 formattedCode,
		"verification_uri":          verificationURI,
		"verification_uri_complete": verificationURI + "?code=" + formattedCode,
		"expires_in":                int(deviceCodeExpiry.Seconds()),
		"interval":                  devicePollInterval,
	})
}

// handleDeviceActivate shows a page where the user enters their user code (or
// auto-fills from ?code=), then redirects to the OAuth provider.
func handleDeviceActivate(w http.ResponseWriter, r *http.Request) {
	codeParam := strings.ReplaceAll(strings.ToUpper(r.URL.Query().Get("code")), "-", "")
	providerParam := r.URL.Query().Get("provider")

	// If a valid code was provided, start the OAuth flow directly when either
	// a provider was selected or there's only one provider configured.
	if codeParam != "" {
		if _, ok := deviceByUser.Load(codeParam); ok {
			if providerParam != "" || len(providers()) == 1 {
				startDeviceOAuth(w, r, codeParam, providerParam)
				return
			}
		}
	}

	const pageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Device Login</title>
<style>
  body{font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f5f5f5}
  .card{background:#fff;border-radius:12px;padding:2.5rem 3rem;box-shadow:0 4px 24px rgba(0,0,0,.08);text-align:center;min-width:320px}
  h1{font-size:1.3rem;margin:0 0 .5rem;color:#111}
  p{color:#666;font-size:.9rem;margin:0 0 1.5rem}
  input[type=text]{width:100%;padding:.75rem;font-size:1.4rem;text-align:center;letter-spacing:.3em;border:2px solid #ddd;border-radius:8px;box-sizing:border-box;text-transform:uppercase}
  input[type=text]:focus{outline:none;border-color:#4f46e5}
  .providers{margin-top:1.2rem}
  button,a.btn{display:block;width:100%;margin:.5rem 0;padding:.75rem;border-radius:8px;background:#4f46e5;color:#fff;text-decoration:none;font-size:.95rem;border:none;cursor:pointer;text-align:center;box-sizing:border-box}
  button:hover,a.btn:hover{background:#4338ca}
  .error{color:#e53e3e;font-size:.85rem;margin-top:.5rem;display:none}
</style>
</head>
<body>
<div class="card">
  <h1>Device Login</h1>
  <p>Enter the code shown in your terminal</p>
  <form id="form" onsubmit="return go()">
    <input type="text" id="code" maxlength="9" placeholder="XXXX-XXXX" value="{{.Code}}" autofocus autocomplete="off">
    <div class="error" id="err">Invalid or expired code</div>
    <div class="providers" id="providers" style="{{if not .Code}}display:none{{end}}">
      {{range .Providers}}<button type="submit" name="provider" value="{{.Name}}">{{.DisplayName}}</button>{{end}}
      {{if eq (len .Providers) 0}}<button type="submit">Continue</button>{{end}}
    </div>
  </form>
</div>
<script>
var inp = document.getElementById('code');
inp.addEventListener('input', function(){
  var v = this.value.replace(/[^A-Za-z0-9]/g,'').toUpperCase();
  if(v.length>8) v=v.slice(0,8);
  if(v.length>4) v=v.slice(0,4)+'-'+v.slice(4);
  this.value=v;
  document.getElementById('providers').style.display = v.replace(/-/g,'').length===8 ? '' : 'none';
  document.getElementById('err').style.display='none';
});
function go(){
  var code = inp.value.replace(/-/g,'');
  if(code.length!==8){document.getElementById('err').style.display='';return false;}
  var provider = document.activeElement && document.activeElement.value;
  var url = '/_oauth/device/activate?code='+code;
  if(provider) url += '&provider='+provider;
  window.location.href = url;
  return false;
}
</script>
</body>
</html>`

	type providerItem struct {
		Name        string
		DisplayName string
	}
	var items []providerItem
	for name, p := range providers() {
		items = append(items, providerItem{Name: name, DisplayName: p.DisplayName()})
	}

	formatted := ""
	if codeParam != "" && len(codeParam) == 8 {
		formatted = codeParam[:4] + "-" + codeParam[4:]
	}

	t := template.Must(template.New("device").Parse(pageTmpl))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, struct {
		Code      string
		Providers []providerItem
	}{Code: formatted, Providers: items})
	return
}

// startDeviceOAuth validates the user code and kicks off the OAuth flow,
// linking the OAuth state back to the device flow entry.
func startDeviceOAuth(w http.ResponseWriter, r *http.Request, userCode, providerName string) {
	if _, ok := deviceByUser.Load(userCode); !ok {
		http.Error(w, "invalid or expired code", http.StatusBadRequest)
		return
	}
	current := providers()
	if providerName == "" {
		// Pick the first (only) provider.
		for name := range current {
			providerName = name
			break
		}
	}
	p, ok := current[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	state := fmt.Sprintf("device-%d", time.Now().UnixNano())
	devicePending.Store(state, userCode)

	http.SetCookie(w, &http.Cookie{
		Name: "fwd_oauth_state", Value: state,
		MaxAge: 300, HttpOnly: true, Path: "/", Domain: cookieDomain,
		SameSite: http.SameSiteLaxMode, Secure: true,
	})
	http.Redirect(w, r, p.AuthCodeURL(state), http.StatusFound)
}

// handleDeviceToken is polled by the CLI to check whether the user has completed
// the OAuth flow. Returns a JWT on success.
func handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	var deviceCode string

	// RFC 8628 §3.4 requires application/x-www-form-urlencoded; also accept
	// JSON for backward compatibility with existing clients.
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err == nil {
			deviceCode = r.FormValue("device_code")
		}
	} else {
		var body struct {
			DeviceCode string `json:"device_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			deviceCode = body.DeviceCode
		}
	}

	if deviceCode == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	val, ok := deviceFlows.Load(deviceCode)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
		return
	}
	entry := val.(*deviceFlowEntry)

	if time.Now().After(entry.ExpiresAt) {
		deviceFlows.Delete(deviceCode)
		deviceByUser.Delete(entry.UserCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
		return
	}

	if !entry.Completed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		return
	}

	// Success — issue a long-lived JWT for CLI use.
	token, err := signForwardJWTWithExpiry(entry.Email, entry.Name, entry.AvatarURL, entry.Provider, cliTokenExpiry)
	if err != nil {
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}

	// Clean up.
	deviceFlows.Delete(deviceCode)
	deviceByUser.Delete(entry.UserCode)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(cliTokenExpiry.Seconds()),
	})
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	host := r.Header.Get("X-Forwarded-Host")
	proto := r.Header.Get("X-Forwarded-Proto")
	uri := r.Header.Get("X-Forwarded-Uri")
	if proto == "" {
		proto = "https"
	}
	originalURL := uri
	if host != "" {
		originalURL = proto + "://" + host + uri
	}
	if originalURL == "" {
		originalURL = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name: "fwd_oauth_redirect", Value: originalURL,
		MaxAge: 300, HttpOnly: true, Path: "/", Domain: cookieDomain,
		SameSite: http.SameSiteLaxMode, Secure: true,
	})
	// Keep the login page on the project subdomain so handleLoginPage's
	// inboundHost() resolves to the project — otherwise the apex host
	// has no project mapping and falls back to the full provider set,
	// silently bypassing each project's enabled_providers whitelist.
	loginBase := forwardAuthBase
	if host != "" {
		loginBase = proto + "://" + host
	}
	http.Redirect(w, r, loginBase+"/_oauth/login", http.StatusFound)
}

// projectMinimalInfo mirrors muvee-server's projectMinimalInfo response shape
// for /api/internal/projects/{id}.
type projectMinimalInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AccessMode string `json:"access_mode"`
	OwnerName  string `json:"owner_name"`
	OwnerEmail string `json:"owner_email"`
}

func fetchProjectInfoInternal(ctx context.Context, projectID string) (*projectMinimalInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		muveeServerURL+"/api/internal/projects/"+url.PathEscape(projectID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("internal project info returned %d", resp.StatusCode)
	}
	var info projectMinimalInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// projectAuthConfig mirrors muvee-server's /api/internal/projects/by-host
// response shape — everything authservice needs to decide which providers a
// project's downstream sign-in flow may use, plus the branding fields that
// drive the forward-auth login page visual.
type projectAuthConfig struct {
	ProjectID        string           `json:"project_id"`
	ProjectName      string           `json:"project_name"`
	DomainPrefix     string           `json:"domain_prefix"`
	EnabledProviders string           `json:"enabled_providers"`
	AuthRequired     bool             `json:"auth_required"`
	AccessMode       string           `json:"access_mode"`
	Branding         projectBranding `json:"branding"`
}

// projectBranding holds the per-project branding overrides plus the
// platform-wide fallbacks used by the forward-auth login template.
type projectBranding struct {
	SiteName           string `json:"site_name"`
	LogoURL            string `json:"logo_url"`
	FaviconURL         string `json:"favicon_url"`
	PrimaryColor       string `json:"primary_color"`
	SidebarBg          string `json:"sidebar_bg"`
	Tagline            string `json:"tagline"`
	Description        string `json:"description"`
	FooterText         string `json:"footer_text"`
	TrustText          string `json:"trust_text"`
	PlatformSiteName   string `json:"platform_site_name"`
	PlatformLogoURL    string `json:"platform_logo_url"`
	PlatformFaviconURL string `json:"platform_favicon_url"`
}

func fetchProjectAuthConfigByHost(ctx context.Context, host string) (*projectAuthConfig, error) {
	q := url.Values{}
	q.Set("host", host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		muveeServerURL+"/api/internal/projects/by-host?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("internal project-by-host returned %d", resp.StatusCode)
	}
	var cfg projectAuthConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// inboundHost returns the user-visible host for this request. Behind Traefik
// the original Host arrives in X-Forwarded-Host; bare requests fall back to
// r.Host.
func inboundHost(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		return strings.ToLower(strings.TrimSpace(h))
	}
	return strings.ToLower(strings.TrimSpace(r.Host))
}

// projectEnabledFwdProviders intersects a project's enabled_providers whitelist
// with the providers actually loaded by this authservice process. Empty
// enabled_providers means "inherit the full fwdProviders set" so existing
// projects keep working with no backfill.
func projectEnabledFwdProviders(enabledProviders string) []auth.Provider {
	allow := func(string) bool { return true }
	if strings.TrimSpace(enabledProviders) != "" {
		set := make(map[string]bool)
		for _, tok := range strings.Split(enabledProviders, ",") {
			name := strings.ToLower(strings.TrimSpace(tok))
			if name != "" {
				set[name] = true
			}
		}
		allow = func(name string) bool { return set[name] }
	}
	// Preserve the canonical display order used elsewhere (google, feishu, ...).
	current := providers()
	order := []string{"google", "feishu", "wecom", "dingtalk"}
	out := make([]auth.Provider, 0, len(current))
	seen := make(map[string]bool)
	for _, name := range order {
		if p, ok := current[name]; ok && allow(name) {
			out = append(out, p)
			seen[name] = true
		}
	}
	for name, p := range current {
		if !seen[name] && allow(name) {
			out = append(out, p)
		}
	}
	return out
}

// handleProviders returns the OAuth providers the SDK should render for the
// inbound project subdomain. Cross-origin friendly (same policy as
// /_oauth/userinfo) so SPAs hosted on the project domain can call this from
// the browser.
func handleProviders(w http.ResponseWriter, r *http.Request) {
	applyUserInfoCORS(w, r)
	host := inboundHost(r)
	if host == "" {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}
	cfg, err := fetchProjectAuthConfigByHost(r.Context(), host)
	if err != nil {
		log.Printf("authservice: project-by-host(%s): %v", host, err)
		http.Error(w, "providers lookup failed", http.StatusBadGateway)
		return
	}
	if cfg == nil {
		http.Error(w, "no project for host", http.StatusNotFound)
		return
	}
	type item struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	}
	providers := projectEnabledFwdProviders(cfg.EnabledProviders)
	out := make([]item, 0, len(providers))
	for _, p := range providers {
		out = append(out, item{Name: p.Name(), DisplayName: p.DisplayName()})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type submitAccessRequestResult struct {
	AlreadyAllowed bool `json:"already_allowed"`
}

func submitAccessRequestInternal(ctx context.Context, projectID, email, reason string) (*submitAccessRequestResult, error) {
	body, err := json.Marshal(map[string]string{
		"project_id": projectID,
		"email":      email,
		"reason":     reason,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		muveeServerURL+"/api/internal/access/submit-request",
		strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("submit access request returned %d", resp.StatusCode)
	}
	var out submitAccessRequestResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// requestAccessPageTmpl renders both the form and the post-submit confirmation
// states of /_oauth/request-access. Kept as a Go template (instead of a SPA
// bundle) because authservice already serves /_oauth/login the same way and
// the page has no client-side state worth pulling in React for.
const requestAccessPageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Request access — {{.ProjectName}}</title>
<style>
  body{font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f5f5f5;padding:1rem}
  .card{background:#fff;border-radius:12px;padding:2.5rem 3rem;box-shadow:0 4px 24px rgba(0,0,0,.08);max-width:520px;width:100%;box-sizing:border-box}
  h1{font-size:1.4rem;margin:0 0 .8rem;color:#111}
  p{color:#555;line-height:1.6;font-size:.95rem;margin:.4rem 0}
  .muted{color:#888;font-size:.85rem}
  textarea{width:100%;min-height:6rem;padding:.65rem;border:1px solid #ddd;border-radius:6px;font:inherit;box-sizing:border-box;resize:vertical}
  textarea:focus{outline:none;border-color:#4f46e5}
  button{padding:.7rem 1.4rem;border-radius:8px;background:#4f46e5;color:#fff;border:none;font-size:.95rem;cursor:pointer;margin-top:1rem}
  button:hover{background:#4338ca}
  .err{color:#b91c1c;background:#fef2f2;border:1px solid #fecaca;padding:.6rem .8rem;border-radius:6px;margin-top:1rem;font-size:.85rem}
  .ok{color:#166534;background:#f0fdf4;border:1px solid #bbf7d0;padding:.6rem .8rem;border-radius:6px;margin-top:1rem;font-size:.9rem}
  a{color:#4f46e5;text-decoration:none}
  a:hover{text-decoration:underline}
</style>
</head>
<body>
<div class="card">
{{if eq .Phase "form"}}
  <h1>Request access</h1>
  <p><strong>{{.ProjectName}}</strong> is private. Send the owner a quick note explaining why you need access — they'll decide and you'll be let in once approved.</p>
  <form method="POST" action="/_oauth/request-access">
    <input type="hidden" name="project_id" value="{{.ProjectID}}">
    <label for="reason" class="muted">Reason (optional)</label>
    <textarea id="reason" name="reason" maxlength="1000" placeholder="What do you need this for?"></textarea>
    <button type="submit">Send request</button>
  </form>
  <p class="muted" style="margin-top:1.2rem">Signed in as {{.Email}}. <a href="/_oauth/logout?redirect=/">Sign out</a></p>
{{else if eq .Phase "submitted"}}
  <h1>Request submitted</h1>
  <div class="ok">We've notified the owner of <strong>{{.ProjectName}}</strong>. You'll be able to reach this project once they approve your request.</div>
  <p class="muted">Signed in as {{.Email}}.</p>
{{else if eq .Phase "already-allowed"}}
  <h1>You already have access</h1>
  <p>{{.ProjectName}} is already accessible from your account. <a href="/">Try opening it again</a> — if it still fails, ask the owner to verify.</p>
  <p class="muted">Signed in as {{.Email}}.</p>
{{else if eq .Phase "error"}}
  <h1>Something went wrong</h1>
  <div class="err">{{.Error}}</div>
  <p class="muted">If this keeps happening, contact the project owner directly.</p>
{{end}}
</div>
</body>
</html>`

// renderRequestAccessPage executes requestAccessPageTmpl with the given fields.
// Kept as a single helper so the GET / POST branches can't drift on look.
func renderRequestAccessPage(w http.ResponseWriter, status int, data map[string]string) {
	t := template.Must(template.New("request-access").Parse(requestAccessPageTmpl))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = t.Execute(w, data)
}

// handleRequestAccessPage renders the request-access form on the project's
// own subdomain. ForwardAuth-deny redirects land here directly so users never
// see the platform domain.
//
// Flow:
//   - If the user has no muvee_fwd_session, save the current URL in
//     fwd_oauth_redirect (mirroring redirectToLogin) and bounce them to
//     /_oauth/login. After OAuth they come back here with a session.
//   - With a session, fetch project info via the internal endpoint and render
//     the form. Errors render an inline error page rather than redirecting,
//     so the URL bar stays predictable for the user.
func handleRequestAccessPage(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project"))
	if projectID == "" {
		renderRequestAccessPage(w, http.StatusBadRequest, map[string]string{
			"Phase": "error", "Error": "Missing ?project=<id> in the URL.",
		})
		return
	}
	claims, err := resolveAuthClaims(r)
	if err != nil {
		// Save full request URL so the user lands back on this page after
		// completing OAuth. Reuses the same cookie redirectToLogin uses.
		host := r.Host
		proto := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
			proto = "http"
		}
		if xfp := r.Header.Get("X-Forwarded-Proto"); xfp != "" {
			proto = xfp
		}
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			host = xfh
		}
		http.SetCookie(w, &http.Cookie{
			Name: "fwd_oauth_redirect",
			Value: proto + "://" + host + "/_oauth/request-access?project=" + url.QueryEscape(projectID),
			MaxAge: 300, HttpOnly: true, Path: "/", Domain: cookieDomain,
			SameSite: http.SameSiteLaxMode, Secure: true,
		})
		http.Redirect(w, r, "/_oauth/login", http.StatusFound)
		return
	}
	info, err := fetchProjectInfoInternal(r.Context(), projectID)
	if err != nil {
		log.Printf("authservice: fetch project info (%s): %v", projectID, err)
		renderRequestAccessPage(w, http.StatusBadGateway, map[string]string{
			"Phase": "error", "Error": "Cannot reach muvee-server. Try again in a moment.",
		})
		return
	}
	if info == nil {
		renderRequestAccessPage(w, http.StatusNotFound, map[string]string{
			"Phase": "error", "Error": "Project not found.",
		})
		return
	}
	renderRequestAccessPage(w, http.StatusOK, map[string]string{
		"Phase":       "form",
		"ProjectID":   info.ID,
		"ProjectName": info.Name,
		"Email":       claims.Email,
	})
}

// handleRequestAccessSubmit accepts the form POST from the request-access
// page and forwards the submission to muvee-server's internal endpoint. The
// caller's email comes from the JWT (not a form field) so a user can't open
// a request on someone else's behalf.
func handleRequestAccessSubmit(w http.ResponseWriter, r *http.Request) {
	claims, err := resolveAuthClaims(r)
	if err != nil {
		http.Redirect(w, r, "/_oauth/login", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		renderRequestAccessPage(w, http.StatusBadRequest, map[string]string{
			"Phase": "error", "Error": "Invalid form submission.",
		})
		return
	}
	projectID := strings.TrimSpace(r.PostFormValue("project_id"))
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if projectID == "" {
		renderRequestAccessPage(w, http.StatusBadRequest, map[string]string{
			"Phase": "error", "Error": "Missing project id.",
		})
		return
	}
	info, err := fetchProjectInfoInternal(r.Context(), projectID)
	if err != nil || info == nil {
		log.Printf("authservice: fetch project info (%s): %v", projectID, err)
		renderRequestAccessPage(w, http.StatusBadGateway, map[string]string{
			"Phase": "error", "Error": "Cannot reach muvee-server. Try again in a moment.",
		})
		return
	}
	res, err := submitAccessRequestInternal(r.Context(), projectID, claims.Email, reason)
	if err != nil {
		log.Printf("authservice: submit access request (project=%s email=%s): %v", projectID, claims.Email, err)
		renderRequestAccessPage(w, http.StatusBadGateway, map[string]string{
			"Phase": "error", "Error": "Could not submit your request. Try again in a moment.",
		})
		return
	}
	phase := "submitted"
	if res.AlreadyAllowed {
		phase = "already-allowed"
	}
	renderRequestAccessPage(w, http.StatusOK, map[string]string{
		"Phase":       phase,
		"ProjectID":   info.ID,
		"ProjectName": info.Name,
		"Email":       claims.Email,
	})
}

func signForwardJWT(email, name, avatarURL, provider string) (string, error) {
	return signForwardJWTWithExpiry(email, name, avatarURL, provider, 7*24*time.Hour)
}

func signForwardJWTWithExpiry(email, name, avatarURL, provider string, expiry time.Duration) (string, error) {
	claims := authClaims{
		Email:     email,
		Name:      name,
		AvatarURL: avatarURL,
		Provider:  provider,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
}

func parseForwardJWT(tokenStr string) (*authClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &authClaims{}, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	c, ok := token.Claims.(*authClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid")
	}
	return c, nil
}

func emailMatchesDomains(email, allowedDomains string) bool {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return false
	}
	domain := parts[1]
	for _, d := range strings.Split(allowedDomains, ",") {
		if strings.TrimSpace(d) == domain {
			return true
		}
	}
	return false
}
