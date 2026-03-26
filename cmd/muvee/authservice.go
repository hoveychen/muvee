package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hoveychen/muvee/internal/auth"
)

type authClaims struct {
	Email string `json:"email"`
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
	r.Get("/_oauth/login", handleLoginPage)
	r.Get("/_oauth/{provider}", handleOAuthCallback)

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

// handleVerify is the Traefik ForwardAuth endpoint for regular users.
func handleVerify(w http.ResponseWriter, r *http.Request) {
	allowedDomains := r.URL.Query().Get("domains")
	cookie, err := r.Cookie("muvee_fwd_session")
	if err != nil {
		redirectToLogin(w, r)
		return
	}
	claims, err := parseForwardJWT(cookie.Value)
	if err != nil {
		redirectToLogin(w, r)
		return
	}
	if allowedDomains != "" {
		if !emailMatchesDomains(claims.Email, allowedDomains) {
			http.Error(w, "access denied: email domain not permitted", http.StatusForbidden)
			return
		}
	}
	w.Header().Set("X-Forwarded-User", claims.Email)
	w.WriteHeader(http.StatusOK)
}

// handleVerifyAdmin is the Traefik ForwardAuth endpoint restricted to admin emails.
func handleVerifyAdmin(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("muvee_fwd_session")
	if err != nil {
		redirectToLogin(w, r)
		return
	}
	claims, err := parseForwardJWT(cookie.Value)
	if err != nil {
		redirectToLogin(w, r)
		return
	}
	if _, ok := adminEmails[claims.Email]; !ok {
		http.Error(w, "access denied: admin only", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Forwarded-User", claims.Email)
	w.WriteHeader(http.StatusOK)
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
	email, _, _, err := p.UserInfo(ctx, code)
	if err != nil {
		log.Printf("authservice: UserInfo (%s): %v", providerName, err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	signed, err := signForwardJWT(email)
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

func signForwardJWT(email string) (string, error) {
	claims := authClaims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour * 7)),
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
