package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

type Service struct {
	// mu guards providers, which is no longer write-once: the Entra provider
	// is (re)built from system_settings at boot and whenever an admin saves
	// /admin/settings, while request handlers read the map concurrently.
	mu             sync.RWMutex
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
		return nil, fmt.Errorf("JWT_SECRET environment variable is required (was empty)")
	}
	if len(secret) < 32 {
		log.Printf("Warning: JWT_SECRET is %d bytes (strongly recommended >= 32)", len(secret))
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

	slackP, err := newSlackProvider("")
	if err != nil {
		return nil, fmt.Errorf("slack provider: %w", err)
	}
	if slackP != nil {
		svc.providers[slackP.Name()] = slackP
	}

	// Entra ID is configured through /admin/settings (with ENTRA_* env
	// fallback), so it is registered from the DB rather than from env alone.
	// A failure here must not stop the server from booting — the remaining
	// providers still work and the admin can fix the tenant/credentials in the
	// UI, which reloads the provider set in place.
	if err := svc.ReloadSettingsProviders(context.Background()); err != nil {
		log.Printf("Warning: entra provider not registered: %v", err)
	}

	if len(svc.providers) == 0 {
		return nil, fmt.Errorf("no auth provider configured; set at least one of GOOGLE_CLIENT_ID, FEISHU_APP_ID, WECOM_CORP_ID, DINGTALK_CLIENT_ID, or enable Entra ID in /admin/settings")
	}
	return svc, nil
}

// provider returns a registered provider under the read lock.
func (s *Service) provider(name string) (Provider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[name]
	return p, ok
}

// providerSnapshot copies the provider map so callers can iterate it without
// holding the lock.
func (s *Service) providerSnapshot() map[string]Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Provider, len(s.providers))
	for k, v := range s.providers {
		out[k] = v
	}
	return out
}

// setProvider registers p under name, or removes the entry when p is nil.
func (s *Service) setProvider(name string, p Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == nil {
		delete(s.providers, name)
		return
	}
	s.providers[name] = p
}

// ReloadSettingsProviders rebuilds the providers that are configured through
// system_settings rather than env vars — today just Microsoft Entra ID, gated
// on platform_entra_login_enabled. Called at boot and after an admin saves
// /admin/settings so the platform login page reflects the change without a
// restart. A build failure (malformed tenant) returns an error and leaves the
// previously registered set untouched.
func (s *Service) ReloadSettingsProviders(ctx context.Context) error {
	settings := map[string]string{}
	if s.store != nil {
		all, err := s.store.GetAllSettings(ctx)
		if err != nil {
			return fmt.Errorf("read settings: %w", err)
		}
		settings = all
	}
	if !platformEntraEnabled(settings) {
		s.setProvider("entra", nil)
		return nil
	}
	p, err := newEntraProvider(EntraConfigFromSettings(settings), platformEntraRedirectURL())
	if err != nil {
		return fmt.Errorf("entra provider: %w", err)
	}
	if p == nil {
		// Toggle on but credentials incomplete: treat as not configured so the
		// login page doesn't offer a button that cannot work.
		s.setProvider("entra", nil)
		return nil
	}
	s.setProvider("entra", p)
	return nil
}

// platformEntraEnabled reads the platform-side Entra toggle: the
// platform_entra_login_enabled setting, falling back to the PLATFORM_ENTRA_LOGIN
// env var when the setting was never written. Off by default.
func platformEntraEnabled(settings map[string]string) bool {
	if v := strings.TrimSpace(settings["platform_entra_login_enabled"]); v != "" {
		return v == "true"
	}
	switch os.Getenv("PLATFORM_ENTRA_LOGIN") {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}

// EntraConfigFromSettings assembles the Entra app from a system_settings map,
// falling back per field to the ENTRA_* env vars so an operator who wired the
// creds into the environment does not have to re-enter them in the admin UI.
// Both planes call this — the platform provider below and muvee-server's
// SocialConfigs assembly for the downstream ForwardAuth path — so a single
// credential set is resolved identically on both sides; only the redirect URI
// differs, and Azure allows both on one app registration.
func EntraConfigFromSettings(settings map[string]string) EntraConfig {
	pick := func(key, env string) string {
		if v := strings.TrimSpace(settings[key]); v != "" {
			return v
		}
		return strings.TrimSpace(os.Getenv(env))
	}
	return EntraConfig{
		TenantID:     pick("entra_tenant_id", "ENTRA_TENANT_ID"),
		ClientID:     pick("entra_client_id", "ENTRA_CLIENT_ID"),
		ClientSecret: pick("entra_client_secret", "ENTRA_CLIENT_SECRET"),
	}
}

// platformEntraRedirectURL is the callback baked into the platform-side Entra
// provider. ENTRA_REDIRECT_URL wins; otherwise it is derived from BASE_DOMAIN
// using the same /auth/{provider}/callback route the other platform providers
// use. Multi-domain deployments rebase this host per request
// (Server.oauthRedirectFor), so only one URI has to be registered in Azure per
// base domain.
func platformEntraRedirectURL() string {
	if u := strings.TrimSpace(os.Getenv("ENTRA_REDIRECT_URL")); u != "" {
		return u
	}
	if base := strings.TrimSpace(os.Getenv("BASE_DOMAIN")); base != "" {
		return "https://" + base + "/auth/entra/callback"
	}
	return "http://localhost:8080/auth/entra/callback"
}

// ListProviders returns the list of enabled identity providers for the frontend.
func (s *Service) ListProviders() []ProviderInfo {
	// Return in a stable order: google, feishu, wecom, dingtalk, slack, others
	order := []string{"google", "feishu", "wecom", "dingtalk", "slack", "entra"}
	providers := s.providerSnapshot()
	var result []ProviderInfo
	seen := make(map[string]bool)
	for _, name := range order {
		if p, ok := providers[name]; ok {
			result = append(result, ProviderInfo{ID: p.Name(), DisplayName: p.DisplayName()})
			seen[name] = true
		}
	}
	for name, p := range providers {
		if !seen[name] {
			result = append(result, ProviderInfo{ID: p.Name(), DisplayName: p.DisplayName()})
		}
	}
	return result
}

// DefaultProvider returns the name of the first available provider (used for CLI auth).
func (s *Service) DefaultProvider() string {
	providers := s.providerSnapshot()
	for _, name := range []string{"google", "feishu", "wecom", "dingtalk", "entra"} {
		if _, ok := providers[name]; ok {
			return name
		}
	}
	for name := range providers {
		return name
	}
	return ""
}

// AuthCodeURL builds the provider's authorize URL. redirectURL overrides the
// provider's baked callback when non-empty so a multi-domain caller can point
// the flow back at whichever base domain the request arrived on; "" keeps the
// baked default. The SAME redirectURL must be handed to HandleCallback.
func (s *Service) AuthCodeURL(providerName, state, redirectURL string) (string, error) {
	p, ok := s.provider(providerName)
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerName)
	}
	return p.AuthCodeURL(state, redirectURL), nil
}

// CanonicalRedirectURL returns the baked callback URL of a registered
// provider, from which a multi-domain caller derives the per-request redirect
// by rebasing its host. Returns an error for an unknown provider.
func (s *Service) CanonicalRedirectURL(providerName string) (string, error) {
	p, ok := s.provider(providerName)
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerName)
	}
	return p.CanonicalRedirectURL(), nil
}

// ErrNotInvited is returned by HandleCallback when access_mode is "invite" and
// the user is neither on the email white-list nor carrying a valid invitation
// link token. The frontend matches on this string to render a friendly
// "contact your administrator" hint instead of a generic 401.
var ErrNotInvited = fmt.Errorf("not invited; please contact your administrator")

// HandleCallback exchanges the OAuth code for a user identity. redirectURL
// must match the value passed to AuthCodeURL for this flow (OAuth2 requires
// the authorize and token-exchange redirect_uri to be identical); "" uses the
// provider's baked default.
func (s *Service) HandleCallback(ctx context.Context, providerName, code, inviteToken, redirectURL string) (*store.User, string, error) {
	p, ok := s.provider(providerName)
	if !ok {
		return nil, "", fmt.Errorf("unknown provider %q", providerName)
	}

	email, name, avatarURL, err := p.UserInfo(ctx, code, redirectURL)
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

// SyntheticPhoneEmail derives the stable pseudo-email used to route a phone
// login through the existing email-keyed platform machinery (UpsertUser,
// EnsurePlatformMember, ADMIN_EMAILS, invite whitelist). The .invalid TLD is
// reserved (RFC 2606) and never routable, so it can never collide with a real
// address; the E.164 digits make it unique per phone number.
func SyntheticPhoneEmail(e164 string) string {
	return strings.TrimPrefix(e164, "+") + "@phone.invalid"
}

// HandlePhoneLogin completes a platform (admin-plane) phone login after the
// verification code has been checked by the caller. It mirrors HandleCallback:
// it synthesises the pseudo-email, runs the same EnsurePlatformMember policy
// (provider "phone" is org-scoped so the domain check is skipped), and signs a
// muvee_session JWT. ErrNotInvited propagates unchanged so the caller can
// surface the invite-mode gate. To make a phone number an admin, add its
// synthetic email (see SyntheticPhoneEmail) to ADMIN_EMAILS.
func (s *Service) HandlePhoneLogin(ctx context.Context, e164 string) (*store.User, string, error) {
	email := SyntheticPhoneEmail(e164)
	user, _, err := s.EnsurePlatformMember(ctx, "phone", email, e164, "", "")
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

// EnsureIdentityFromOAuth is the social-login counterpart to EnsureIdentity:
// it keys identity on (providerName, providerUserID) via
// store.EnsureUserByOAuth so providers whose IdP does not surface an email
// (Discord, Twitter free tier, Apple Hide-My-Email never-shared) can still
// resolve to a stable users row. The users.email column stays NULL for
// these users; downstream code paths that rely on email (e.g.
// IsProjectAccessAllowedByEmail) will not find them — they must be granted
// access via the user_id-keyed project_access_users table.
//
// Identity-only: never writes platform_members. Used by the subdomain
// ForwardAuth handler when the provider is one of the configured social
// providers.
func (s *Service) EnsureIdentityFromOAuth(ctx context.Context, providerName, providerUserID, name, avatarURL string) (*store.User, error) {
	user, _, err := s.store.EnsureUserByOAuth(ctx, providerName, providerUserID, name, avatarURL)
	if err != nil {
		return nil, fmt.Errorf("ensure user by oauth: %w", err)
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
// Project-scoped invitation links short-circuit the platform-level policy
// entirely: they admit a downstream-only user into a single project, so the
// platform's ALLOWED_DOMAINS gate and the platform_members upsert do not
// apply. The caller still gets back a *User (sans *PlatformMember) so the
// upstream upsert endpoint can respond with the same JSON shape.
//
// providerName is consulted only to skip the domain check for org-scoped
// providers; it falls back to a hard-coded list when the provider is not
// registered locally so authservice-only providers still get the right
// treatment.
func (s *Service) EnsurePlatformMember(ctx context.Context, providerName, email, name, avatarURL, inviteToken string) (*store.User, *store.PlatformMember, error) {
	// Resolve the invite token BEFORE any platform-level gate so a
	// project-scoped link can short-circuit ALLOWED_DOMAINS / invite-mode
	// checks that don't apply to downstream-only invitees. The token may be
	// a platform-scoped single-use link OR a project-scoped multi-use link;
	// only the project-scoped flavour takes the early-return below — the
	// platform-scoped one still threads through the standard checks since
	// it does grant platform access.
	var consumeLink *store.InvitationLink
	if inviteToken != "" {
		link, err := s.store.GetValidInvitationLinkByHash(ctx, hashInviteToken(inviteToken), time.Now())
		if err != nil {
			return nil, nil, fmt.Errorf("check invite link: %w", err)
		}
		consumeLink = link
	}

	// Project-scoped invite token: write the identity row + invitation_link_uses
	// + project_access_users and return. The user becomes a downstream-project
	// member only; we intentionally do NOT create a platform_members row, do
	// NOT enforce ALLOWED_DOMAINS, and do NOT consult access_mode — those are
	// platform-side admission rules that have nothing to do with letting an
	// outsider into a single project they were invited to.
	if consumeLink != nil && consumeLink.ProjectID != nil {
		user, err := s.EnsureIdentity(ctx, email, name, avatarURL)
		if err != nil {
			return nil, nil, err
		}
		if err := s.store.RecordInvitationLinkUse(ctx, consumeLink.ID, user.ID); err != nil {
			return nil, nil, fmt.Errorf("record invitation link use: %w", err)
		}
		if err := s.store.AddProjectAccessUser(ctx, *consumeLink.ProjectID, user.ID, consumeLink.InvitedBy); err != nil {
			return nil, nil, fmt.Errorf("add project access user: %w", err)
		}
		return user, nil, nil
	}

	if !isOrgScopedProvider(s.providerSnapshot(), providerName) {
		if err := s.checkDomain(email); err != nil {
			return nil, nil, err
		}
	}

	mode := s.accessMode(ctx)
	_, isAdmin := s.adminEmails[email]

	// Pre-flight checks for invite mode: figure out whether this email is
	// invited (white-list), holds a valid link, or — for already existing
	// platform members — gets to keep signing in regardless. New platform
	// members matching none of these are rejected without ever being created.
	emailInvited := false
	if mode == store.AccessModeInvite && !isAdmin {
		invited, err := s.store.IsEmailInvited(ctx, email)
		if err != nil {
			return nil, nil, fmt.Errorf("check invitation: %w", err)
		}
		emailInvited = invited
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

	// Platform-scoped single-use link consumption. Project-scoped links are
	// already handled by the early-return at the top of the function, so
	// anything reaching this point is platform-scoped. Best-effort: a
	// concurrent login may have already consumed the link; we've already
	// promoted the platform member above, so swallow the error.
	if consumeLink != nil {
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
	// "entra" is deliberately absent from this fallback: whether an Entra app
	// is org-scoped depends on its tenant (a GUID / verified-domain tenant is,
	// the multi-tenant aliases are not), which is only knowable from the
	// registered provider above. Unregistered, we keep the stricter behaviour
	// and still run the domain check.
	case "phone":
		// A verified phone number is a self-contained identity with no email
		// domain; the domain whitelist has nothing to match against, so phone
		// login skips checkDomain and is governed by access_mode / invite /
		// ADMIN_EMAILS (keyed on the synthetic phone email) instead.
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
