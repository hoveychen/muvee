package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Service struct {
	oauth2Config   *oauth2.Config
	verifier       *gooidc.IDTokenVerifier
	jwtSecret      []byte
	allowedDomains []string
	adminEmails    map[string]struct{}
	store          *store.Store
}

type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

type contextKey string

const CtxUserKey contextKey = "user"

func New(st *store.Store) (*Service, error) {
	ctx := context.Background()
	provider, err := gooidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirectURL := os.Getenv("GOOGLE_REDIRECT_URL")
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/auth/google/callback"
	}
	allowedDomains := strings.Split(os.Getenv("ALLOWED_DOMAINS"), ",")
	var filtered []string
	for _, d := range allowedDomains {
		d = strings.TrimSpace(d)
		if d != "" {
			filtered = append(filtered, d)
		}
	}
	adminEmails := make(map[string]struct{})
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			adminEmails[e] = struct{}{}
		}
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "change-me-in-production"
	}
	return &Service{
		oauth2Config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     google.Endpoint,
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
		verifier:       provider.Verifier(&gooidc.Config{ClientID: clientID}),
		jwtSecret:      []byte(secret),
		allowedDomains: filtered,
		adminEmails:    adminEmails,
		store:          st,
	}, nil
}

func (s *Service) AuthCodeURL(state string) string {
	return s.oauth2Config.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

func (s *Service) HandleCallback(ctx context.Context, code string) (*store.User, string, error) {
	token, err := s.oauth2Config.Exchange(ctx, code)
	if err != nil {
		return nil, "", fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, "", fmt.Errorf("no id_token")
	}
	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, "", fmt.Errorf("verify token: %w", err)
	}
	var claims struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, "", fmt.Errorf("parse claims: %w", err)
	}
	if err := s.checkDomain(claims.Email); err != nil {
		return nil, "", err
	}
	user, err := s.store.UpsertUser(ctx, claims.Email, claims.Name, claims.Picture)
	if err != nil {
		return nil, "", fmt.Errorf("upsert user: %w", err)
	}
	// Auto-promote users listed in ADMIN_EMAILS on every login.
	if _, isAdmin := s.adminEmails[claims.Email]; isAdmin && user.Role != store.UserRoleAdmin {
		if err := s.store.SetUserRole(ctx, user.ID, store.UserRoleAdmin); err != nil {
			return nil, "", fmt.Errorf("promote admin: %w", err)
		}
		user.Role = store.UserRoleAdmin
	}
	jwt, err := s.signJWT(user)
	if err != nil {
		return nil, "", err
	}
	return user, jwt, nil
}

func (s *Service) checkDomain(email string) error {
	if len(s.allowedDomains) == 0 {
		return nil
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid email")
	}
	domain := parts[1]
	for _, d := range s.allowedDomains {
		if d == domain {
			return nil
		}
	}
	return fmt.Errorf("email domain %q not allowed", domain)
}

func (s *Service) signJWT(user *store.User) (string, error) {
	claims := Claims{
		UserID: user.ID.String(),
		Email:  user.Email,
		Role:   string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour * 7)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

func (s *Service) ParseJWT(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// CreateAPIToken generates a new random API token for a user, stores its hash, and returns the token.
func (s *Service) CreateAPIToken(ctx context.Context, userID uuid.UUID, name string) (*store.ApiToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	tokenStr := "mvt_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tokenStr))
	hashHex := hex.EncodeToString(hash[:])

	t, err := s.store.CreateAPIToken(ctx, userID, name, hashHex)
	if err != nil {
		return nil, err
	}
	t.Token = tokenStr
	return t, nil
}

func (s *Service) lookupAPIToken(ctx context.Context, tokenStr string) (*store.User, error) {
	hash := sha256.Sum256([]byte(tokenStr))
	hashHex := hex.EncodeToString(hash[:])
	apiToken, err := s.store.GetAPITokenByHash(ctx, hashHex)
	if err != nil || apiToken == nil {
		return nil, fmt.Errorf("invalid token")
	}
	return s.store.GetUserByID(ctx, apiToken.UserID)
}

// Middleware injects the authenticated user into the request context.
// Accepts both JWT session tokens and long-lived API tokens (prefix "mvt_").
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := extractToken(r)
		if tokenStr == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var user *store.User

		if strings.HasPrefix(tokenStr, "mvt_") {
			// API token path
			var err error
			user, err = s.lookupAPIToken(r.Context(), tokenStr)
			if err != nil || user == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else {
			// JWT path
			claims, err := s.ParseJWT(tokenStr)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			user, err = s.store.GetUserByID(r.Context(), userID)
			if err != nil || user == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		ctx := context.WithValue(r.Context(), CtxUserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminOnly requires the Admin role.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromCtx(r.Context())
		if user == nil || user.Role != store.UserRoleAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func UserFromCtx(ctx context.Context) *store.User {
	u, _ := ctx.Value(CtxUserKey).(*store.User)
	return u
}

func extractToken(r *http.Request) string {
	bearer := r.Header.Get("Authorization")
	if strings.HasPrefix(bearer, "Bearer ") {
		return strings.TrimPrefix(bearer, "Bearer ")
	}
	cookie, err := r.Cookie("muvee_session")
	if err == nil {
		return cookie.Value
	}
	return ""
}
