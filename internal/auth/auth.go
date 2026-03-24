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

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

type Service struct {
	providers      map[string]Provider
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

// ProviderInfo is returned by ListProviders for the frontend to render login buttons.
type ProviderInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func New(st *store.Store) (*Service, error) {
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

	svc := &Service{
		providers:      make(map[string]Provider),
		jwtSecret:      []byte(secret),
		allowedDomains: filtered,
		adminEmails:    adminEmails,
		store:          st,
	}

	// Register all configured providers (pass "" to use each provider's own *_REDIRECT_URL env var).
	googleP, err := newGoogleProvider("")
	if err != nil {
		return nil, fmt.Errorf("google provider: %w", err)
	}
	if googleP != nil {
		svc.providers[googleP.Name()] = googleP
	}

	feishuP, err := newFeishuProvider("")
	if err != nil {
		return nil, fmt.Errorf("feishu provider: %w", err)
	}
	if feishuP != nil {
		svc.providers[feishuP.Name()] = feishuP
	}

	wecomP, err := newWeComProvider("")
	if err != nil {
		return nil, fmt.Errorf("wecom provider: %w", err)
	}
	if wecomP != nil {
		svc.providers[wecomP.Name()] = wecomP
	}

	dingtalkP, err := newDingTalkProvider("")
	if err != nil {
		return nil, fmt.Errorf("dingtalk provider: %w", err)
	}
	if dingtalkP != nil {
		svc.providers[dingtalkP.Name()] = dingtalkP
	}

	if len(svc.providers) == 0 {
		return nil, fmt.Errorf("no auth provider configured; set at least one of GOOGLE_CLIENT_ID, FEISHU_APP_ID, WECOM_CORP_ID, DINGTALK_CLIENT_ID")
	}
	return svc, nil
}

// ListProviders returns the list of enabled identity providers for the frontend.
func (s *Service) ListProviders() []ProviderInfo {
	// Return in a stable order: google, feishu, wecom, dingtalk, others
	order := []string{"google", "feishu", "wecom", "dingtalk"}
	var result []ProviderInfo
	seen := make(map[string]bool)
	for _, name := range order {
		if p, ok := s.providers[name]; ok {
			result = append(result, ProviderInfo{ID: p.Name(), DisplayName: p.DisplayName()})
			seen[name] = true
		}
	}
	for name, p := range s.providers {
		if !seen[name] {
			result = append(result, ProviderInfo{ID: p.Name(), DisplayName: p.DisplayName()})
		}
	}
	return result
}

// DefaultProvider returns the name of the first available provider (used for CLI auth).
func (s *Service) DefaultProvider() string {
	for _, name := range []string{"google", "feishu", "wecom", "dingtalk"} {
		if _, ok := s.providers[name]; ok {
			return name
		}
	}
	for name := range s.providers {
		return name
	}
	return ""
}

func (s *Service) AuthCodeURL(providerName, state string) (string, error) {
	p, ok := s.providers[providerName]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerName)
	}
	return p.AuthCodeURL(state), nil
}

func (s *Service) HandleCallback(ctx context.Context, providerName, code string) (*store.User, string, error) {
	p, ok := s.providers[providerName]
	if !ok {
		return nil, "", fmt.Errorf("unknown provider %q", providerName)
	}

	email, name, avatarURL, err := p.UserInfo(ctx, code)
	if err != nil {
		return nil, "", fmt.Errorf("user info: %w", err)
	}

	// Only enforce domain restrictions for providers that produce real email addresses.
	// Synthetic emails (*.local) bypass the check since the provider itself is already
	// scoped to a specific organisation.
	if !strings.HasSuffix(email, ".local") {
		if err := s.checkDomain(email); err != nil {
			return nil, "", err
		}
	}

	user, err := s.store.UpsertUser(ctx, email, name, avatarURL)
	if err != nil {
		return nil, "", fmt.Errorf("upsert user: %w", err)
	}
	// Auto-promote users listed in ADMIN_EMAILS on every login.
	if _, isAdmin := s.adminEmails[email]; isAdmin && user.Role != store.UserRoleAdmin {
		if err := s.store.SetUserRole(ctx, user.ID, store.UserRoleAdmin); err != nil {
			return nil, "", fmt.Errorf("promote admin: %w", err)
		}
		user.Role = store.UserRoleAdmin
	}
	jwtToken, err := s.signJWT(user)
	if err != nil {
		return nil, "", err
	}
	return user, jwtToken, nil
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
// If projectID is non-nil the token is scoped to that project.
func (s *Service) CreateAPIToken(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, name string) (*store.ApiToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	tokenStr := "mvt_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tokenStr))
	hashHex := hex.EncodeToString(hash[:])

	t, err := s.store.CreateAPIToken(ctx, userID, projectID, name, hashHex)
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
