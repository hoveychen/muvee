package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
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
	fwdProviders   map[string]auth.Provider
	jwtSecret      []byte
	adminEmails    map[string]struct{}
	cookieDomain   string
	forwardAuthBase string // e.g. "https://example.com"
)

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
		secret = "change-me-in-production"
	}
	jwtSecret = []byte(secret)

	adminEmails = make(map[string]struct{})
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			adminEmails[e] = struct{}{}
		}
	}

	var err error
	fwdProviders, err = auth.NewForwardAuthProviders(baseURL)
	if err != nil {
		log.Fatalf("init auth providers: %v", err)
	}
	if len(fwdProviders) == 0 {
		log.Fatal("no auth providers configured; set at least one of GOOGLE_CLIENT_ID, FEISHU_APP_ID, WECOM_CORP_ID, DINGTALK_CLIENT_ID")
	}

	r := chi.NewRouter()
	r.Get("/verify", handleVerify)
	r.Get("/verify-admin", handleVerifyAdmin)
	r.Get("/_oauth/userinfo", handleUserInfo)
	r.Get("/_oauth/logout", handleFwdLogout)
	r.Get("/_oauth/login", handleLoginPage)
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
	for n := range fwdProviders {
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
	claims, err := resolveAuthClaims(r)
	if err != nil {
		redirectToLogin(w, r)
		return
	}
	if allowedDomains := r.URL.Query().Get("domains"); allowedDomains != "" {
		if !emailMatchesDomains(claims.Email, allowedDomains) {
			http.Error(w, "access denied: email domain not permitted", http.StatusForbidden)
			return
		}
	}
	setUserHeaders(w, claims)
	w.WriteHeader(http.StatusOK)
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
// it kicks off the OAuth flow for that specific provider.
func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	providerName := r.URL.Query().Get("provider")
	if providerName != "" {
		p, ok := fwdProviders[providerName]
		if !ok {
			http.Error(w, "unknown provider", http.StatusBadRequest)
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
	if len(fwdProviders) == 1 {
		for name := range fwdProviders {
			http.Redirect(w, r, "/_oauth/login?provider="+name, http.StatusFound)
			return
		}
	}

	// Multiple providers: render a simple selection page.
	type providerItem struct {
		Name        string
		DisplayName string
	}
	var items []providerItem
	for name, p := range fwdProviders {
		items = append(items, providerItem{Name: name, DisplayName: p.DisplayName()})
	}

	const pageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in</title>
<style>
  body{font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f5f5f5}
  .card{background:#fff;border-radius:12px;padding:2.5rem 3rem;box-shadow:0 4px 24px rgba(0,0,0,.08);text-align:center;min-width:280px}
  h1{font-size:1.3rem;margin:0 0 1.5rem;color:#111}
  a.btn{display:block;margin:.6rem 0;padding:.75rem 1.5rem;border-radius:8px;background:#4f46e5;color:#fff;text-decoration:none;font-size:.95rem;transition:background .15s}
  a.btn:hover{background:#4338ca}
</style>
</head>
<body>
<div class="card">
  <h1>Sign in to continue</h1>
  {{range .}}<a class="btn" href="/_oauth/login?provider={{.Name}}">{{.DisplayName}}</a>{{end}}
</div>
</body>
</html>`

	t := template.Must(template.New("login").Parse(pageTmpl))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, items)
}

// handleOAuthCallback handles the OAuth redirect back from each provider.
func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	providerName := chi.URLParam(r, "provider")
	p, ok := fwdProviders[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	stateCookie, err := r.Cookie("fwd_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
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
			if providerParam != "" || len(fwdProviders) == 1 {
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
	var providers []providerItem
	for name, p := range fwdProviders {
		providers = append(providers, providerItem{Name: name, DisplayName: p.DisplayName()})
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
	}{Code: formatted, Providers: providers})
	return
}

// startDeviceOAuth validates the user code and kicks off the OAuth flow,
// linking the OAuth state back to the device flow entry.
func startDeviceOAuth(w http.ResponseWriter, r *http.Request, userCode, providerName string) {
	if _, ok := deviceByUser.Load(userCode); !ok {
		http.Error(w, "invalid or expired code", http.StatusBadRequest)
		return
	}
	if providerName == "" {
		// Pick the first (only) provider.
		for name := range fwdProviders {
			providerName = name
			break
		}
	}
	p, ok := fwdProviders[providerName]
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
	http.Redirect(w, r, forwardAuthBase+"/_oauth/login", http.StatusFound)
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
