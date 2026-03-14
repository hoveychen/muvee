package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type authClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

var (
	oauth2Cfg    *oauth2.Config
	oidcVerifier *gooidc.IDTokenVerifier
	jwtSecret    []byte
	adminEmails  map[string]struct{}
)

func main() {
	ctx := context.Background()
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirectURL := os.Getenv("FORWARD_AUTH_REDIRECT_URL")
	if redirectURL == "" {
		redirectURL = "http://localhost:4181/_oauth"
	}
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

	provider, err := gooidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		log.Fatalf("oidc provider: %v", err)
	}
	oidcVerifier = provider.Verifier(&gooidc.Config{ClientID: clientID})
	oauth2Cfg = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
	}

	r := chi.NewRouter()
	r.Get("/verify", handleVerify)
	r.Get("/verify-admin", handleVerifyAdmin)
	r.Get("/_oauth", handleOAuthCallback)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4181"
	}
	log.Printf("ForwardAuth service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

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

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stateCookie, err := r.Cookie("fwd_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	token, err := oauth2Cfg.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "exchange error", http.StatusInternalServerError)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token", http.StatusInternalServerError)
		return
	}
	idToken, err := oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "token verify failed", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = idToken.Claims(&claims)
	signed, err := signForwardJWT(claims.Email)
	if err != nil {
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "muvee_fwd_session", Value: signed,
		MaxAge: 7 * 24 * 3600, HttpOnly: true, Path: "/", SameSite: http.SameSiteLaxMode,
	})
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	state := fmt.Sprintf("%d", time.Now().UnixNano())
	http.SetCookie(w, &http.Cookie{Name: "fwd_oauth_state", Value: state, MaxAge: 300, HttpOnly: true, Path: "/"})
	redirect := r.Header.Get("X-Forwarded-Uri")
	authURL := oauth2Cfg.AuthCodeURL(state, oauth2.SetAuthURLParam("redirect_uri",
		oauth2Cfg.RedirectURL+"?redirect="+redirect))
	http.Redirect(w, r, authURL, http.StatusFound)
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
