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

// ErrNotInvited is returned by HandleCallback when access_mode is "invite" and
// the user is neither on the email white-list nor carrying a valid invitation
// link token. The frontend matches on this string to render a friendly
// "contact your administrator" hint instead of a generic 401.
var ErrNotInvited = fmt.Errorf("not invited; please contact your administrator")

func (s *Service) HandleCallback(ctx context.Context, providerName, code, inviteToken string) (*store.User, string, error) {
	p, ok := s.providers[providerName]
	if !ok {
		return nil, "", fmt.Errorf("unknown provider %q", providerName)
	}

	email, name, avatarURL, err := p.UserInfo(ctx, code)
	if err != nil {
		return nil, "", fmt.Errorf("user info: %w", err)
	}

	// Skip domain restrictions for org-scoped providers (Feishu, WeCom, DingTalk).
	// These providers inherently restrict users to a specific organisation, so the
	// email domain check is redundant — regardless of whether the email is real or synthetic.
	if !p.OrgScoped() {
		if err := s.checkDomain(email); err != nil {
			return nil, "", err
		}
	}

	mode := s.accessMode(ctx)
	_, isAdmin := s.adminEmails[email]

	// Pre-flight checks for invite mode: figure out whether this email is
	// invited (white-list), holds a valid one-time link, or — for already
	// existing accounts — gets to keep signing in regardless. New accounts
	// matching none of these are rejected without ever being created.
	emailInvited := false
	var consumeLink *store.InvitationLink
	if mode == store.AccessModeInvite && !isAdmin {
		invited, err := s.store.IsEmailInvited(ctx, email)
		if err != nil {
			return nil, "", fmt.Errorf("check invitation: %w", err)
		}
		emailInvited = invited
		if !emailInvited && inviteToken != "" {
			link, err := s.store.GetValidInvitationLinkByHash(ctx, hashInviteToken(inviteToken), time.Now())
			if err != nil {
				return nil, "", fmt.Errorf("check invite link: %w", err)
			}
			consumeLink = link
		}
		if !emailInvited && consumeLink == nil {
			existing, err := s.store.GetUserByEmail(ctx, email)
			if err != nil {
				return nil, "", fmt.Errorf("lookup user: %w", err)
			}
			if existing == nil {
				return nil, "", ErrNotInvited
			}
		}
	}

	// In request mode, brand-new non-admin accounts default to authorized=FALSE
	// so they have to go through the request flow. Other modes default to TRUE
	// for new accounts (invite mode reaches here only when invited or
	// link-consumed; open mode is unrestricted). UpsertUser only applies this
	// on INSERT — existing rows preserve their flag.
	authorizedOnInsert := true
	if mode == store.AccessModeRequest && !isAdmin {
		authorizedOnInsert = false
	}

	user, _, err := s.store.UpsertUser(ctx, email, name, avatarURL, authorizedOnInsert)
	if err != nil {
		return nil, "", fmt.Errorf("upsert user: %w", err)
	}

	// Auto-promote users listed in ADMIN_EMAILS on every login.
	if isAdmin && user.Role != store.UserRoleAdmin {
		if err := s.store.SetUserRole(ctx, user.ID, store.UserRoleAdmin); err != nil {
			return nil, "", fmt.Errorf("promote admin: %w", err)
		}
		user.Role = store.UserRoleAdmin
	}

	// In invite mode, an existing-but-unauthorized user being newly invited (or
	// arriving with a valid link) should be flipped to authorized on this login.
	if mode == store.AccessModeInvite && !user.Authorized && (emailInvited || consumeLink != nil) {
		if err := s.store.SetUserAuthorized(ctx, user.ID, true); err != nil {
			return nil, "", fmt.Errorf("authorize user: %w", err)
		}
		user.Authorized = true
	}

	if consumeLink != nil {
		// Best-effort: a concurrent login may have already consumed the link.
		// We've already promoted the user above, so swallow the error.
		_ = s.store.ConsumeInvitationLink(ctx, consumeLink.ID, user.ID)
	}

	jwtToken, err := s.signJWT(user)
	if err != nil {
		return nil, "", err
	}
	return user, jwtToken, nil
}

// accessMode returns the current AccessMode setting, defaulting to Open when
// unset or unreadable so a misconfigured DB doesn't lock everyone out.
func (s *Service) accessMode(ctx context.Context) store.AccessMode {
	v, err := s.store.GetSetting(ctx, "access_mode")
	if err != nil || v == "" {
		return store.AccessModeOpen
	}
	switch store.AccessMode(v) {
	case store.AccessModeOpen, store.AccessModeInvite, store.AccessModeRequest:
		return store.AccessMode(v)
	}
	return store.AccessModeOpen
}

// HashInviteToken returns the sha256 hex digest of an invitation-link token,
// used for both writes (storage) and reads (lookups).
func HashInviteToken(token string) string { return hashInviteToken(token) }

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
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

// Token prefixes. `mvt_` is used for project-scoped API tokens; `mvp_` is used
// for personal access tokens (project_id IS NULL) created by users for
// programmatic access (e.g. AI agents). Legacy CLI-login tokens also use the
// `mvt_` prefix with project_id NULL — those are accepted for backwards
// compatibility.
const (
	tokenPrefixProject = "mvt_"
	tokenPrefixUser    = "mvp_"
)

func isAPITokenPrefix(s string) bool {
	return strings.HasPrefix(s, tokenPrefixProject) || strings.HasPrefix(s, tokenPrefixUser)
}

// isTokenExpired reports whether an ApiToken.ExpiresAt timestamp is past.
// A nil ExpiresAt means the token never expires. Exactly-equal-to-now counts
// as expired (strict "after" comparison).
func isTokenExpired(expiresAt *time.Time, now time.Time) bool {
	if expiresAt == nil {
		return false
	}
	return !expiresAt.After(now)
}

// CreateAPIToken generates a new random API token for a user, stores its hash, and returns the token.
// If projectID is non-nil the token is scoped to that project and uses the
// `mvt_` prefix; otherwise it's a personal access token and uses `mvp_`.
// expiresAt is optional: nil means never expires.
func (s *Service) CreateAPIToken(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, name string, expiresAt *time.Time) (*store.ApiToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	prefix := tokenPrefixProject
	if projectID == nil {
		prefix = tokenPrefixUser
	}
	tokenStr := prefix + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tokenStr))
	hashHex := hex.EncodeToString(hash[:])

	t, err := s.store.CreateAPIToken(ctx, userID, projectID, name, hashHex, expiresAt)
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
	if isTokenExpired(apiToken.ExpiresAt, time.Now()) {
		return nil, fmt.Errorf("token expired")
	}
	return s.store.GetUserByID(ctx, apiToken.UserID)
}

// Middleware injects the authenticated user into the request context.
// Accepts JWT session tokens and long-lived API tokens (prefixes "mvt_" / "mvp_").
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := extractToken(r)
		if tokenStr == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var user *store.User

		if isAPITokenPrefix(tokenStr) {
			// API token path (personal or project-scoped)
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
