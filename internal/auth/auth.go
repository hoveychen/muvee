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

const (
	CtxUserKey           contextKey = "user"
	CtxPlatformMemberKey contextKey = "platform_member"
)

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

	user, _, err := s.EnsurePlatformMember(ctx, providerName, email, name, avatarURL, inviteToken)
	if err != nil {
		return nil, "", err
	}

	jwtToken, err := s.signJWT(user)
	if err != nil {
		return nil, "", err
	}
	return user, jwtToken, nil
}

// EnsureIdentity upserts the user row for an already-verified
// (email, name, avatarURL) triple. It does NOT enforce platform-side policy:
// no domain check, no invite gate, no admin promotion, no platform_members
// row. Callers that only need identity (e.g. the subdomain ForwardAuth
// handler in cmd/muvee/authservice — those users may never become platform
// members) should use this path.
//
// Subdomain users still need to exist in `users` so per-project access
// checks like IsProjectAccessAllowedByEmail can resolve them.
func (s *Service) EnsureIdentity(ctx context.Context, email, name, avatarURL string) (*store.User, error) {
	// authorizedOnInsert is a legacy mirror that survives migration 033; the
	// real authorization signal lives in platform_members and is set by
	// EnsurePlatformMember. Pass TRUE so existing UpsertUser SQL keeps
	// satisfying the NOT NULL constraint without flagging anything new.
	user, _, err := s.store.UpsertUser(ctx, email, name, avatarURL, true)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return user, nil
}

// EnsurePlatformMember runs the full set of post-OAuth platform-side policy
// rules on an already-verified (email, name, avatarURL) triple: domain
// restrictions for non-org-scoped providers, invite-mode gating, request-mode
// authorization defaults, identity upsert, admin auto-promotion, and one-time
// invitation link consumption. The user is also upserted into
// platform_members so /admin/users sees them and the requireAuthorized
// middleware can recognise them. Returns ErrNotInvited when access_mode is
// "invite" and the email is neither white-listed, link-bearing, nor an
// existing account.
//
// providerName is consulted only to skip the domain check for org-scoped
// providers; it falls back to a hard-coded list when the provider is not
// registered locally so authservice-only providers still get the right
// treatment.
func (s *Service) EnsurePlatformMember(ctx context.Context, providerName, email, name, avatarURL, inviteToken string) (*store.User, *store.PlatformMember, error) {
	if !isOrgScopedProvider(s.providers, providerName) {
		if err := s.checkDomain(email); err != nil {
			return nil, nil, err
		}
	}

	mode := s.accessMode(ctx)
	_, isAdmin := s.adminEmails[email]

	// Pre-flight checks for invite mode: figure out whether this email is
	// invited (white-list), holds a valid one-time link, or — for already
	// existing platform members — gets to keep signing in regardless. New
	// platform members matching none of these are rejected without ever
	// being created.
	emailInvited := false
	var consumeLink *store.InvitationLink
	if mode == store.AccessModeInvite && !isAdmin {
		invited, err := s.store.IsEmailInvited(ctx, email)
		if err != nil {
			return nil, nil, fmt.Errorf("check invitation: %w", err)
		}
		emailInvited = invited
		if !emailInvited && inviteToken != "" {
			link, err := s.store.GetValidInvitationLinkByHash(ctx, hashInviteToken(inviteToken), time.Now())
			if err != nil {
				return nil, nil, fmt.Errorf("check invite link: %w", err)
			}
			consumeLink = link
		}
		if !emailInvited && consumeLink == nil {
			// Existing platform members can keep signing in even after the
			// invite list shifts; identity-only rows (came in through
			// subdomain auth) do NOT count — they were never granted
			// platform access in the first place.
			existing, err := s.store.GetUserByEmail(ctx, email)
			if err != nil {
				return nil, nil, fmt.Errorf("lookup user: %w", err)
			}
			if existing == nil {
				return nil, nil, ErrNotInvited
			}
			pm, err := s.store.GetPlatformMember(ctx, existing.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("lookup platform member: %w", err)
			}
			if pm == nil {
				return nil, nil, ErrNotInvited
			}
		}
	}

	// In request mode, brand-new non-admin platform members default to
	// authorized=FALSE so they have to go through the request flow.
	// UpsertPlatformMember only applies this on INSERT — existing rows
	// preserve their flag.
	authorizedOnInsert := true
	if mode == store.AccessModeRequest && !isAdmin {
		authorizedOnInsert = false
	}

	user, err := s.EnsureIdentity(ctx, email, name, avatarURL)
	if err != nil {
		return nil, nil, err
	}

	roleOnInsert := store.UserRoleMember
	if isAdmin {
		roleOnInsert = store.UserRoleAdmin
	}
	pm, _, err := s.store.UpsertPlatformMember(ctx, user.ID, roleOnInsert, authorizedOnInsert)
	if err != nil {
		return nil, nil, fmt.Errorf("upsert platform member: %w", err)
	}

	// Auto-promote ADMIN_EMAILS on every login (handles existing rows that
	// were created as 'member' before being added to ADMIN_EMAILS).
	if isAdmin && pm.Role != store.UserRoleAdmin {
		if err := s.store.SetPlatformMemberRole(ctx, user.ID, store.UserRoleAdmin); err != nil {
			return nil, nil, fmt.Errorf("promote admin: %w", err)
		}
		pm.Role = store.UserRoleAdmin
	}

	// In invite mode, an existing-but-unauthorized platform member being
	// newly invited (or arriving with a valid link) should be flipped to
	// authorized on this login.
	if mode == store.AccessModeInvite && !pm.Authorized && (emailInvited || consumeLink != nil) {
		if err := s.store.SetPlatformMemberAuthorized(ctx, user.ID, true); err != nil {
			return nil, nil, fmt.Errorf("authorize platform member: %w", err)
		}
		pm.Authorized = true
	}

	if consumeLink != nil {
		// Best-effort: a concurrent login may have already consumed the link.
		// We've already promoted the platform member above, so swallow the
		// error.
		_ = s.store.ConsumeInvitationLink(ctx, consumeLink.ID, user.ID)
	}

	// Mirror platform_member values back onto the legacy user.Role /
	// user.Authorized fields so JSON responses and JWT claims that still
	// read from the User struct stay correct during the rollout window.
	// Migration 034 will drop these columns; this mirroring goes away then.
	user.Role = pm.Role
	user.Authorized = pm.Authorized

	return user, pm, nil
}

// EnsureUser is preserved as a thin wrapper for backwards compatibility; it
// runs the full platform path and returns only the user. New code should
// call EnsurePlatformMember (for platform-side login flows) or
// EnsureIdentity (for subdomain ForwardAuth flows) directly.
//
// Deprecated: use EnsurePlatformMember or EnsureIdentity instead.
func (s *Service) EnsureUser(ctx context.Context, providerName, email, name, avatarURL, inviteToken string) (*store.User, error) {
	user, _, err := s.EnsurePlatformMember(ctx, providerName, email, name, avatarURL, inviteToken)
	return user, err
}

// isOrgScopedProvider reports whether the named provider inherently restricts
// users to a specific organisation. Looks up the live provider when registered;
// otherwise falls back to the canonical list so authservice-only providers
// still skip the domain check when invoked through EnsureUser.
func isOrgScopedProvider(providers map[string]Provider, name string) bool {
	if p, ok := providers[name]; ok {
		return p.OrgScoped()
	}
	switch name {
	case "feishu", "wecom", "dingtalk":
		return true
	}
	return false
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
		// Load the platform_members row alongside the user so handlers can
		// answer "is this caller a platform admin / authorized to write?"
		// without an extra round-trip. Identity-only users (came in through
		// subdomain auth) get nil here; PlatformRoleFromCtx and
		// PlatformAuthorizedFromCtx then return their zero values.
		if pm, err := s.store.GetPlatformMember(r.Context(), user.ID); err == nil && pm != nil {
			ctx = context.WithValue(ctx, CtxPlatformMemberKey, pm)
			// Mirror onto the legacy User fields so JSON responses and JWT
			// claims still see correct values during the rollout window.
			user.Role = pm.Role
			user.Authorized = pm.Authorized
		} else {
			// No platform_member row → strip any stale legacy values so
			// downstream code that mistakenly reads user.Role / user.Authorized
			// can't accidentally let an identity-only user past a platform gate.
			user.Role = store.UserRoleMember
			user.Authorized = false
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminOnly requires the platform admin role.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PlatformRoleFromCtx(r.Context()) != store.UserRoleAdmin {
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

// PlatformMemberFromCtx returns the platform_members row for the request's
// authenticated user, or nil for identity-only users (came in through
// subdomain auth and never crossed over to the muvee admin plane).
func PlatformMemberFromCtx(ctx context.Context) *store.PlatformMember {
	pm, _ := ctx.Value(CtxPlatformMemberKey).(*store.PlatformMember)
	return pm
}

// PlatformRoleFromCtx returns the caller's platform role, or "" when they
// are not a platform member.
func PlatformRoleFromCtx(ctx context.Context) store.UserRole {
	pm := PlatformMemberFromCtx(ctx)
	if pm == nil {
		return ""
	}
	return pm.Role
}

// PlatformAuthorizedFromCtx reports whether the caller is a platform member
// authorized to perform write operations (admins are always authorized;
// non-members are never authorized regardless of any legacy users.authorized
// value).
func PlatformAuthorizedFromCtx(ctx context.Context) bool {
	pm := PlatformMemberFromCtx(ctx)
	if pm == nil {
		return false
	}
	if pm.Role == store.UserRoleAdmin {
		return true
	}
	return pm.Authorized
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
