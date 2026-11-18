package main

// Per-subsite session isolation with a central sign-in host
// (BACKLOG-20261117-007, design wiki muvee/auth/fwd-session-isolation-design).
//
// Three credentials:
//   - the subsite session (__Host-muvee_fwd): host-only on each project /
//     tunnel / dashboard host, kind=session, aud=project:<id>|host:<h>, its own
//     sid, plus sso_sid and auth_time of the SSO session it was minted from;
//   - the central SSO session (__Host-muvee_sso): host-only on the central
//     sign-in host of each base domain (forwardAuthBaseForHost), kind=sso,
//     aud=sso:<central host>; never sent to a tenant and only used to mint
//     one-time codes for subsites;
//   - CLI device-flow tokens: kind=cli, aud of the subsite that started them.
//
// A subsite with no session sends the browser to the central host
// (/_oauth/authorize, prompt=none). With a live SSO the central host mints a
// 60 s single-use code bound to the target host, its audience and the
// subsite's state; the subsite exchanges it (/_oauth/session) for its own
// session. Without one the subsite renders its login page; a provider click
// goes through /_oauth/start on the central host, whose callback signs the SSO
// and continues as above. Signing out of one subsite revokes its sid and the
// sso_sid (fwd_session_revocations), which ends every session minted from
// that SSO on its next request.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoveychen/muvee/internal/domains"
)

// ─── Lifetimes ──────────────────────────────────────────────────────────────

const (
	// subsiteSessionLifetime / subsiteSessionRenewAfter: a subsite session
	// slides in 12 h windows and is re-signed once less than 6 h are left.
	subsiteSessionLifetime   = 12 * time.Hour
	subsiteSessionRenewAfter = 6 * time.Hour
	// projectSessionLifetime is the fixed lifetime of a password / phone
	// session (no SSO behind it, never renewed: verify does not re-check the
	// demo account or phone grant, so the fixed expiry is what retires it).
	projectSessionLifetime = 7 * 24 * time.Hour
	// ssoLifetime / ssoRenewAfter: the central SSO slides in 7-day windows,
	// re-signed at most once a day, always keeping its original auth_time.
	ssoLifetime   = 7 * 24 * time.Hour
	ssoRenewAfter = 24 * time.Hour
	// ssoCodeLifetime bounds the central → subsite authorization code.
	ssoCodeLifetime = 60 * time.Second
	// loginStateLifetime bounds the subsite / central state cookies and the
	// post-login return URL cookie. Generous on purpose: users do park on the
	// provider's page (a 43-minute pause in production turned into a 400 at
	// 10 minutes); the state stays single-use and host-bound either way.
	loginStateLifetime = 60 * time.Minute
	// maxLoginRoundTrips: failed code exchanges re-enter the login page with a
	// counter; past this many the user gets an error page instead of a loop.
	// The central host's recovery (recoverCentralSignIn) uses the same bound.
	maxLoginRoundTrips = 2
	// maxStateSlots bounds how many concurrent sign-ins one browser keeps a
	// state cookie for per host (each sign-in has its own cookie, see
	// stateSlotBase); starting one more drops the oldest.
	maxStateSlots = 4
	// loginHintLifetime / loginRecoveryWindow: the central host remembers the
	// last sign-in target (fwd_login_hint) so a callback whose state is gone
	// can send the browser back there to start over; recoveries within one
	// window are counted against maxLoginRoundTrips.
	loginHintLifetime   = ssoLifetime
	loginRecoveryWindow = 10 * time.Minute
)

const fwdKindSSO = "sso"

// ─── Cookies ────────────────────────────────────────────────────────────────

const (
	fwdSessionCookieBase  = "muvee_fwd"
	ssoCookieBase         = "muvee_sso"
	fwdStateCookieBase    = "fwd_state"
	apexStateCookieBase   = "fwd_apex_state"
	signedOutCookieBase   = "muvee_fwd_signed_out"
	fwdRedirectCookieBase = "fwd_oauth_redirect"
	loginHintCookieBase   = "fwd_login_hint"
)

// slotCookieBases are the state cookies written once per sign-in under a
// derived name (base + "_" + slot, see stateSlotBase).
var slotCookieBases = []string{fwdStateCookieBase, apexStateCookieBase}

// stripPrefixWildcard marks a strip-list entry that matches every cookie name
// starting with the text before it (embed-bridge stripCookies semantics).
const stripPrefixWildcard = "*"

// legacyFwdCookies are the base-domain cookies earlier releases set. They are
// never written any more; sign-out, a rejected session and a completed
// sign-in delete them (Domain=<base>) so they stop riding along to tenants.
var legacyFwdCookies = []string{
	"muvee_fwd_session", "fwd_oauth_state", "fwd_oauth_host",
	"muvee_invite_token", "muvee_fwd_signed_out", "fwd_oauth_redirect",
}

// fwdAllCookieNames lists every auth cookie name that must never reach a
// tenant: the __Host- names, their plain-http dev fallbacks (the bare base
// names) and the legacy base-domain names. Per-sign-in state cookies have a
// derived suffix, so they are listed as prefix entries ("__Host-fwd_state_*").
// internal/api FwdAuthCookieNames, which the HTTP provider passes to every
// embed-bridge instance, must hold the same set (a drift-guard test compares
// them).
func fwdAllCookieNames() []string {
	bases := []string{fwdSessionCookieBase, ssoCookieBase, fwdStateCookieBase, apexStateCookieBase, signedOutCookieBase, fwdRedirectCookieBase, loginHintCookieBase}
	out := []string{}
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, b := range bases {
		add("__Host-" + b)
	}
	for _, b := range bases {
		add(b)
	}
	for _, n := range legacyFwdCookies {
		add(n)
	}
	for _, b := range slotCookieBases {
		add("__Host-" + b + "_" + stripPrefixWildcard)
		add(b + "_" + stripPrefixWildcard)
	}
	return out
}

// devInsecureCookies drops the __Host- prefix and the Secure flag. Only ever
// true on a plain-http platform (FORWARD_AUTH_BASE_URL http://) with
// FWD_INSECURE_DEV_COOKIES=1: browsers refuse __Host- cookies over http.
var devInsecureCookies bool

func initFwdCookieMode() {
	devInsecureCookies = strings.HasPrefix(forwardAuthBase, "http://") && os.Getenv("FWD_INSECURE_DEV_COOKIES") == "1"
}

func fwdCookieName(base string) string {
	if devInsecureCookies {
		return base
	}
	return "__Host-" + base
}

// setHostCookie writes a host-only cookie (no Domain, Path=/, HttpOnly,
// Secure, SameSite=Lax) — the __Host- contract. maxAge<0 deletes it.
func setHostCookie(w http.ResponseWriter, base, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: fwdCookieName(base), Value: value, MaxAge: maxAge,
		Path: "/", HttpOnly: true, Secure: !devInsecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func hostCookie(r *http.Request, base string) (string, bool) {
	c, err := r.Cookie(fwdCookieName(base))
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// clearLegacyFwdCookies deletes the pre-isolation base-domain cookies.
func clearLegacyFwdCookies(w http.ResponseWriter, r *http.Request) {
	d := cookieDomainForRequest(r)
	for _, name := range legacyFwdCookies {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", MaxAge: -1, Path: "/", Domain: d, HttpOnly: true})
	}
}

// ─── Sealed values (state cookies) ──────────────────────────────────────────

func sealMAC(purpose string, payload []byte) []byte {
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = mac.Write([]byte("muvee-seal\x00" + purpose + "\x00"))
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

// sealValue serialises v with an issued-at time and signs it for purpose.
func sealValue(purpose string, v any) string {
	b, _ := json.Marshal(struct {
		T int64 `json:"t"`
		V any   `json:"v"`
	}{time.Now().Unix(), v})
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sealMAC(purpose, b))
}

// openValue verifies a sealValue for purpose that is at most maxAge old.
func openValue(purpose, s string, maxAge time.Duration, v any) bool {
	_, ok := openValueAt(purpose, s, maxAge, v)
	return ok
}

// openValueAt is openValue that also returns the seal's issue time.
func openValueAt(purpose, s string, maxAge time.Duration, v any) (time.Time, bool) {
	i := strings.IndexByte(s, '.')
	if i <= 0 {
		return time.Time{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(s[:i])
	if err != nil {
		return time.Time{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(s[i+1:])
	if err != nil || !hmac.Equal(sig, sealMAC(purpose, b)) {
		return time.Time{}, false
	}
	var env struct {
		T int64           `json:"t"`
		V json.RawMessage `json:"v"`
	}
	if json.Unmarshal(b, &env) != nil {
		return time.Time{}, false
	}
	issued := time.Unix(env.T, 0)
	age := time.Since(issued)
	if age < -time.Minute || age > maxAge {
		return time.Time{}, false
	}
	return issued, json.Unmarshal(env.V, v) == nil
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func stateDigest(state string) string {
	h := sha256.Sum256([]byte("muvee-sso-state\x00" + state))
	return hex.EncodeToString(h[:16])
}

// subsiteState is the __Host-fwd_state cookie: the subsite half of a sign-in.
type subsiteState struct {
	Host   string `json:"h"`
	Nonce  string `json:"n"`
	Trips  int    `json:"c,omitempty"`
	Popup  bool   `json:"p,omitempty"`
	Device bool   `json:"d,omitempty"`
}

// apexState is the __Host-fwd_apex_state cookie on the central host.
type apexState struct {
	Host     string `json:"h"`
	Aud      string `json:"a"`
	State    string `json:"s,omitempty"` // subsite state (login mode)
	Nonce    string `json:"n"`
	Mode     string `json:"m"` // "login" | "device"
	UserCode string `json:"u,omitempty"`
	Provider string `json:"p"`
}

// ─── Per-sign-in state slots ────────────────────────────────────────────────
//
// Every sign-in keeps its state in its own cookie, named base + "_" + a short
// hash of the value the callback carries back (the subsite nonce, the
// central f1). Two sign-ins started from one browser — a second tab, a
// prefetched duplicate of the login chain — no longer overwrite each other,
// and a callback only ever reads and deletes its own slot. The names are
// public; what vouches for a callback is still the sealed value under a
// __Host- name, which no sibling subdomain can write.

const maxStateKeyLen = 128

// stateSlotBase is the cookie base name of the state slot for key.
func stateSlotBase(base, key string) string {
	h := sha256.Sum256([]byte("muvee-state-slot\x00" + base + "\x00" + key))
	return base + "_" + hex.EncodeToString(h[:6])
}

// pruneStateSlots runs before a new slot of base is written: it deletes this
// browser's slots that are expired or unreadable, the pre-slot single cookie,
// and the oldest live ones beyond maxStateSlots-1 so the request header stays
// bounded however many sign-ins get abandoned.
func pruneStateSlots(w http.ResponseWriter, r *http.Request, base, purpose string) {
	prefix := fwdCookieName(base) + "_"
	type slot struct {
		base   string
		issued time.Time
	}
	var live []slot
	for _, c := range r.Cookies() {
		if c.Name == fwdCookieName(base) {
			setHostCookie(w, base, "", -1)
			continue
		}
		if !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		slotBase := base + "_" + strings.TrimPrefix(c.Name, prefix)
		var discard json.RawMessage
		issued, ok := openValueAt(purpose, c.Value, loginStateLifetime, &discard)
		if !ok {
			setHostCookie(w, slotBase, "", -1)
			continue
		}
		live = append(live, slot{slotBase, issued})
	}
	for len(live) > maxStateSlots-1 {
		oldest := 0
		for i := range live {
			if live[i].issued.Before(live[oldest].issued) {
				oldest = i
			}
		}
		setHostCookie(w, live[oldest].base, "", -1)
		live = append(live[:oldest], live[oldest+1:]...)
	}
}

// writeSubsiteState starts a subsite sign-in: a fresh nonce (carrying the
// round-trip counter after a ".", so the loop bound survives a lost cookie)
// sealed into its own slot. Returns the nonce, which travels as state.
func writeSubsiteState(w http.ResponseWriter, r *http.Request, trips int, popup bool) string {
	nonce := randomToken(18) + "." + strconv.Itoa(trips)
	st := subsiteState{Host: domains.NormalizeHost(inboundHost(r)), Nonce: nonce, Trips: trips, Popup: popup}
	pruneStateSlots(w, r, fwdStateCookieBase, "fwd-state")
	setHostCookie(w, stateSlotBase(fwdStateCookieBase, nonce), sealValue("fwd-state", st), int(loginStateLifetime.Seconds()))
	return nonce
}

// readSubsiteState returns the subsite sign-in whose nonce is the returned
// state, from that sign-in's own slot. It does not delete the slot.
func readSubsiteState(r *http.Request, nonce string) (subsiteState, bool) {
	var st subsiteState
	if nonce == "" || len(nonce) > maxStateKeyLen {
		return st, false
	}
	v, ok := hostCookie(r, stateSlotBase(fwdStateCookieBase, nonce))
	if !ok || !openValue("fwd-state", v, loginStateLifetime, &st) {
		return subsiteState{}, false
	}
	if st.Host != domains.NormalizeHost(inboundHost(r)) || !hmac.Equal([]byte(st.Nonce), []byte(nonce)) {
		return subsiteState{}, false
	}
	return st, true
}

// clearSubsiteState deletes the slot of nonce (and nothing else).
func clearSubsiteState(w http.ResponseWriter, nonce string) {
	if nonce != "" && len(nonce) <= maxStateKeyLen {
		setHostCookie(w, stateSlotBase(fwdStateCookieBase, nonce), "", -1)
	}
}

// nonceTrips is the round-trip counter writeSubsiteState put in a nonce, for
// a callback whose slot is gone. Untrusted, so only ever used to stop a loop
// sooner: missing or malformed reads as 0, larger values are capped.
func nonceTrips(nonce string) int {
	i := strings.LastIndexByte(nonce, '.')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(nonce[i+1:])
	if err != nil || n < 0 {
		return 0
	}
	if n > maxLoginRoundTrips+1 {
		return maxLoginRoundTrips + 1
	}
	return n
}

// ─── Tokens ─────────────────────────────────────────────────────────────────

// parseFwdToken validates an HS256 token signed by this service for audience
// aud and one of kinds. Console JWTs (no aud), pre-isolation subsite sessions
// (no aud), SSO tokens presented as a subsite session and vice versa all fail.
func parseFwdToken(tokenStr, aud string, kinds ...string) (*authClaims, error) {
	if aud == "" {
		return nil, errors.New("no audience for this request")
	}
	token, err := jwt.ParseWithClaims(tokenStr, &authClaims{}, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(aud), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	c, ok := token.Claims.(*authClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	for _, k := range kinds {
		if c.Kind == k {
			return c, nil
		}
	}
	return nil, fmt.Errorf("token kind %q not accepted", c.Kind)
}

// fwdIdentity is the identity part shared by every credential.
type fwdIdentity struct {
	UserID, Email, Name, AvatarURL, Provider string
}

func signFwdClaims(c authClaims, lifetime time.Duration) (string, error) {
	now := time.Now()
	c.AvatarURL = cookieSafeAvatar(c.AvatarURL)
	c.IssuedAt = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(now.Add(lifetime))
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(jwtSecret)
}

func identityClaims(id fwdIdentity) authClaims {
	return authClaims{Email: id.Email, UserID: id.UserID, Name: id.Name, AvatarURL: id.AvatarURL, Provider: id.Provider}
}

// signSubsiteSession signs a roaming subsite session minted from an SSO.
func signSubsiteSession(id fwdIdentity, aud, ssoSID string, authTime int64) (string, error) {
	c := identityClaims(id)
	c.Kind, c.SID, c.SSOSID, c.AuthTime = fwdKindSession, randomToken(16), ssoSID, authTime
	c.Audience = jwt.ClaimStrings{aud}
	return signFwdClaims(c, subsiteSessionLifetime)
}

// signProjectSession signs a password / phone session: bound to its project
// (ProjectID and aud), no SSO behind it, auth_time = its own issue time.
func signProjectSession(email, name, avatarURL, provider, projectID string) (string, error) {
	c := authClaims{Email: email, Name: name, AvatarURL: avatarURL, Provider: provider, ProjectID: projectID}
	c.Kind, c.SID, c.AuthTime = fwdKindSession, randomToken(16), time.Now().Unix()
	c.Audience = jwt.ClaimStrings{"project:" + projectID}
	return signFwdClaims(c, projectSessionLifetime)
}

// signSSOSession signs the central SSO session. authTime is kept across
// renewals so console logout (compared against auth_time) still voids it.
func signSSOSession(id fwdIdentity, centralHost, sid string, authTime int64) (string, error) {
	c := identityClaims(id)
	c.Kind, c.SID, c.AuthTime = fwdKindSSO, sid, authTime
	c.Subject = id.UserID
	c.Audience = jwt.ClaimStrings{ssoAudience(centralHost)}
	return signFwdClaims(c, ssoLifetime)
}

// signCLIToken signs a device-flow Bearer token for one subsite audience.
func signCLIToken(id fwdIdentity, aud string) (string, error) {
	c := identityClaims(id)
	c.Kind, c.SID, c.AuthTime = fwdKindCLI, randomToken(16), time.Now().Unix()
	c.Audience = jwt.ClaimStrings{aud}
	return signFwdClaims(c, cliTokenExpiry)
}

func ssoAudience(centralHost string) string { return "sso:" + domains.NormalizeHost(centralHost) }

// claimsAuthTime is the sign-in time console logout is compared against.
func claimsAuthTime(c *authClaims) int64 {
	if c.AuthTime != 0 {
		return c.AuthTime
	}
	return sessionIssuedAt(c)
}

func claimsSIDs(c *authClaims) []string {
	var out []string
	for _, s := range []string{c.SID, c.SSOSID} {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ─── Audiences ──────────────────────────────────────────────────────────────

// verifyAudience is the audience a ForwardAuth /verify accepts: the project
// the router belongs to, or, for a tunnel with no bound project, the tunnel
// host prefix the server baked into the verify URL. Never X-Forwarded-Host.
func verifyAudience(r *http.Request) string {
	if pid := strings.TrimSpace(r.URL.Query().Get("project_id")); pid != "" {
		return "project:" + pid
	}
	if th := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tunnel_host"))); th != "" {
		return "host:" + th
	}
	return ""
}

// routeAudienceHeader is injected by the Traefik headers middleware on every
// subsite /_oauth router (customRequestHeaders overwrite any client value)
// and cleared on the central host's routers.
const routeAudienceHeader = "X-Muvee-Aud"

// routeAudience is the audience Traefik injected for this subsite route. The
// central sign-in host is never a subsite: there the header is ignored even if
// a misconfigured router let a client-sent value through.
func routeAudience(r *http.Request) string {
	if isCentralHost(r) {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(routeAudienceHeader))
}

// adminAudience is the audience /verify-admin accepts: the dashboard host
// traefik.<base> of the base domain the request is on. X-Forwarded-Host is
// set by Traefik on websecure (forwarded headers from clients are not
// trusted), so it is the real dashboard host here.
func adminAudience(r *http.Request) string {
	h := domains.NormalizeHost(r.Header.Get("X-Forwarded-Host"))
	for _, b := range fwdBaseDomains() {
		if h == "traefik."+b {
			return "host:" + h
		}
	}
	return ""
}

func fwdBaseDomains() []string {
	if len(cookieBaseDomains) > 0 {
		return cookieBaseDomains
	}
	if cookieDomain != "" {
		return []string{cookieDomain}
	}
	return nil
}

// ─── Central host ───────────────────────────────────────────────────────────

// centralHostFor is the central sign-in host for host's base domain.
func centralHostFor(host string) string {
	u, err := url.Parse(forwardAuthBaseForHost(host))
	if err != nil {
		return ""
	}
	return domains.NormalizeHost(u.Host)
}

// isCentralHost reports whether the request arrived on its base domain's
// central sign-in host.
func isCentralHost(r *http.Request) bool {
	h := domains.NormalizeHost(inboundHost(r))
	return h != "" && h == centralHostFor(h)
}

func platformScheme() string {
	if strings.HasPrefix(forwardAuthBase, "http://") {
		return "http"
	}
	return "https"
}

// signInTarget is a host the central host may mint a code for.
type signInTarget struct {
	Host string
	Aud  string
	Cfg  *projectAuthConfig // nil: no project (dashboard, project-less tunnel)
}

var errBadTarget = errors.New("not a sign-in host of this base domain")

// resolveSignInTarget validates host for the central host central: it must be
// a muvee-routed host under the same base domain (a project or base-domain
// alias, a live tunnel, or the platform's traefik.<base> dashboard). Hosts
// outside the base domain — custom-domain aliases included — are refused.
func resolveSignInTarget(ctx context.Context, host, central string) (*signInTarget, error) {
	host = domains.NormalizeHost(host)
	bases := fwdBaseDomains()
	cb, ok := domains.Match(central, bases)
	if !ok {
		return nil, errBadTarget
	}
	hb, ok := domains.Match(host, bases)
	if !ok || hb != cb || host == hb || host == central {
		return nil, errBadTarget
	}
	if host == "traefik."+hb {
		return &signInTarget{Host: host, Aud: "host:" + host}, nil
	}
	cfg, aud, err := fetchSignInTargetByHost(ctx, host)
	if err != nil {
		return nil, err
	}
	if aud == "" {
		return nil, errBadTarget
	}
	return &signInTarget{Host: host, Aud: aud, Cfg: cfg}, nil
}

// providerAllowed reports whether provider may sign in to target (a
// project-less host allows every loaded provider).
func (t *signInTarget) providerAllowed(provider string) bool {
	_, ok := allowedProvidersFromConfig(t.Cfg)[provider]
	return ok
}

// ssoCode is a central → subsite authorization code (process memory: the
// authservice runs as a single replica).
type ssoCode struct {
	Host, Aud, StateDigest string
	SSOSID                 string
	AuthTime               int64
	ID                     fwdIdentity
	Expires                time.Time
}

var ssoCodes sync.Map // code → *ssoCode

func mintSSOCode(c *ssoCode) string {
	code := randomToken(32)
	c.Expires = time.Now().Add(ssoCodeLifetime)
	ssoCodes.Store(code, c)
	time.AfterFunc(ssoCodeLifetime+time.Second, func() { ssoCodes.Delete(code) })
	return code
}

func takeSSOCode(code string) (*ssoCode, bool) {
	if code == "" {
		return nil, false
	}
	v, ok := ssoCodes.LoadAndDelete(code)
	if !ok {
		return nil, false
	}
	c := v.(*ssoCode)
	return c, time.Now().Before(c.Expires)
}

// currentSSO returns the request's valid, unrevoked SSO session on the central
// host, or nil. A revoked or invalid one is deleted.
func currentSSO(w http.ResponseWriter, r *http.Request, central string) (*authClaims, error) {
	v, ok := hostCookie(r, ssoCookieBase)
	if !ok {
		return nil, nil
	}
	c, err := parseFwdToken(v, ssoAudience(central), fwdKindSSO)
	if err != nil {
		setHostCookie(w, ssoCookieBase, "", -1)
		return nil, nil
	}
	revoked, err := checkSessionRevoked(r.Context(), c)
	if err != nil {
		return nil, err
	}
	if revoked {
		setHostCookie(w, ssoCookieBase, "", -1)
		return nil, nil
	}
	// Sliding renewal, keeping sid and auth_time.
	if c.ExpiresAt != nil && time.Until(c.ExpiresAt.Time) < ssoLifetime-ssoRenewAfter {
		id := fwdIdentity{UserID: c.UserID, Email: c.Email, Name: c.Name, AvatarURL: c.AvatarURL, Provider: c.Provider}
		if signed, err := signSSOSession(id, central, c.SID, c.AuthTime); err == nil {
			setHostCookie(w, ssoCookieBase, signed, int(ssoLifetime.Seconds()))
		}
	}
	return c, nil
}

func redirectToSubsite(w http.ResponseWriter, r *http.Request, host, path string, q url.Values) {
	http.Redirect(w, r, platformScheme()+"://"+host+path+"?"+q.Encode(), http.StatusFound)
}

// handleSSOAuthorize — GET /_oauth/authorize?host=&state=&prompt=none on the
// central host. Only host and state are read: the target's audience and
// project come from the server, never from parameters.
func handleSSOAuthorize(w http.ResponseWriter, r *http.Request) {
	if !isCentralHost(r) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	central := domains.NormalizeHost(inboundHost(r))
	state := r.URL.Query().Get("state")
	if state == "" || len(state) > 128 {
		http.Error(w, "missing or invalid state", http.StatusBadRequest)
		return
	}
	target, err := resolveSignInTarget(r.Context(), r.URL.Query().Get("host"), central)
	if err != nil {
		log.Printf("authservice: authorize refused host %q: %v", r.URL.Query().Get("host"), err)
		http.Error(w, "unknown sign-in host", http.StatusBadRequest)
		return
	}
	sso, err := currentSSO(w, r, central)
	if err != nil {
		log.Printf("authservice: authorize SSO revocation check: %v", err)
		http.Error(w, "session check failed", http.StatusBadGateway)
		return
	}
	if sso == nil || !target.providerAllowed(sso.Provider) {
		redirectToSubsite(w, r, target.Host, "/_oauth/login", url.Values{"sso": {"none"}, "state": {state}})
		return
	}
	code := mintSSOCode(&ssoCode{
		Host: target.Host, Aud: target.Aud, StateDigest: stateDigest(state),
		SSOSID: sso.SID, AuthTime: claimsAuthTime(sso),
		ID: fwdIdentity{UserID: sso.UserID, Email: sso.Email, Name: sso.Name, AvatarURL: sso.AvatarURL, Provider: sso.Provider},
	})
	redirectToSubsite(w, r, target.Host, "/_oauth/session", url.Values{"code": {code}, "state": {state}})
}

// handleSSOStart — GET /_oauth/start?host=&state=&provider=[&mode=device&user_code=]
// on the central host: keep the sign-in context in the central host's own
// state cookie and send the browser to the provider with f1 as its state.
func handleSSOStart(w http.ResponseWriter, r *http.Request) {
	if !isCentralHost(r) {
		http.NotFound(w, r)
		return
	}
	central := domains.NormalizeHost(inboundHost(r))
	q := r.URL.Query()
	target, err := resolveSignInTarget(r.Context(), q.Get("host"), central)
	if err != nil {
		log.Printf("authservice: start refused host %q: %v", q.Get("host"), err)
		http.Error(w, "unknown sign-in host", http.StatusBadRequest)
		return
	}
	providerName := q.Get("provider")
	p, ok := providers()[providerName]
	if !ok || !target.providerAllowed(providerName) {
		http.Error(w, "unknown or disabled provider", http.StatusBadRequest)
		return
	}
	st := apexState{Host: target.Host, Aud: target.Aud, Nonce: randomToken(18), Provider: providerName, Mode: "login"}
	if q.Get("mode") == "device" {
		uc := normalizeUserCode(q.Get("user_code"))
		entry, ok := deviceEntryByUserCode(uc)
		if !ok || entry.Aud != target.Aud || entry.Host != target.Host {
			http.Error(w, "invalid or expired code", http.StatusBadRequest)
			return
		}
		st.Mode, st.UserCode = "device", uc
	} else {
		st.State = q.Get("state")
		if st.State == "" || len(st.State) > 128 {
			http.Error(w, "missing or invalid state", http.StatusBadRequest)
			return
		}
	}
	f1 := apexStateF1(st.Nonce)
	pruneStateSlots(w, r, apexStateCookieBase, "apex-state")
	setHostCookie(w, stateSlotBase(apexStateCookieBase, f1), sealValue("apex-state", st), int(loginStateLifetime.Seconds()))
	if st.Mode == "login" {
		// Keep the recovery count of an ongoing recovery (see
		// recoverCentralSignIn); only a completed sign-in resets it.
		hint := loginHint{Host: target.Host}
		if prev, ok := readLoginHint(r); ok {
			hint.N, hint.R = prev.N, prev.R
		}
		writeLoginHint(w, hint)
	}
	http.Redirect(w, r, p.AuthCodeURL(f1, oauthRedirectForHost(central, providerName)), http.StatusFound)
}

// readApexState returns the central sign-in context matching the provider's
// state from that sign-in's own slot, deleting the slot (single use). Other
// sign-ins' slots are left alone.
func readApexState(w http.ResponseWriter, r *http.Request, queryState string) (apexState, bool) {
	var st apexState
	if !strings.HasPrefix(queryState, fwdStatePrefix) || len(queryState) > maxStateKeyLen {
		return st, false
	}
	slot := stateSlotBase(apexStateCookieBase, queryState)
	v, ok := hostCookie(r, slot)
	if !ok {
		return st, false
	}
	setHostCookie(w, slot, "", -1)
	if !openValue("apex-state", v, loginStateLifetime, &st) || !apexStateF1Matches(queryState, st.Nonce) {
		return apexState{}, false
	}
	return st, true
}

// loginHint is the central host's __Host-fwd_login_hint: the subsite the last
// browser sign-in was for, and how many automatic recoveries (N, the last at
// unix time R) ran since a sign-in last completed. It only ever decides where
// a failed callback sends the browser to start over — never whether a
// callback is accepted.
type loginHint struct {
	Host string `json:"h"`
	N    int    `json:"n,omitempty"`
	R    int64  `json:"r,omitempty"`
}

func readLoginHint(r *http.Request) (loginHint, bool) {
	var h loginHint
	v, ok := hostCookie(r, loginHintCookieBase)
	if !ok || !openValue("login-hint", v, loginHintLifetime, &h) || h.Host == "" {
		return loginHint{}, false
	}
	return h, true
}

func writeLoginHint(w http.ResponseWriter, h loginHint) {
	setHostCookie(w, loginHintCookieBase, sealValue("login-hint", h), int(loginHintLifetime.Seconds()))
}

// recoverCentralSignIn answers a browser callback whose state cannot be
// matched (expired, lost, another browser's, forged). The provider's code is
// never used: the browser is sent back to the last sign-in target's login
// entry to start a fresh sign-in of its own — with the provider usually
// still signed in, that is a quick bounce. The target comes only from the
// central host's own sealed __Host- hint and must still pass
// resolveSignInTarget (a muvee-routed host of this base domain), so this is no
// open redirect; at most maxLoginRoundTrips recoveries run per
// loginRecoveryWindow without a completed sign-in, after which — or with no
// usable hint — a page explains what to do instead of a bare 400.
func recoverCentralSignIn(w http.ResponseWriter, r *http.Request, reason string) {
	w.Header().Set("Cache-Control", "no-store")
	central := domains.NormalizeHost(inboundHost(r))
	h, ok := readLoginHint(r)
	if ok && time.Since(time.Unix(h.R, 0)) > loginRecoveryWindow {
		h.N = 0
	}
	if ok && h.N < maxLoginRoundTrips {
		target, err := resolveSignInTarget(r.Context(), h.Host, central)
		if err == nil {
			h.N++
			h.R = time.Now().Unix()
			writeLoginHint(w, h)
			log.Printf("authservice: callback state %s on %s; restarting sign-in on %s (%d/%d)", reason, central, target.Host, h.N, maxLoginRoundTrips)
			http.Redirect(w, r, platformScheme()+"://"+target.Host+"/_oauth/login", http.StatusFound)
			return
		}
		log.Printf("authservice: callback state %s on %s; hint host %q refused: %v", reason, central, h.Host, err)
	} else {
		log.Printf("authservice: callback state %s on %s; no recovery (hint=%v)", reason, central, ok)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = signInExpiredTmpl.Execute(w, loginMessages(acceptLang(r)))
}

// signInExpiredTmpl is the central host's page for a callback it cannot
// recover: the central host serves no login page of its own, so it points the
// user back to the site they were signing in to.
var signInExpiredTmpl = template.Must(template.New("signin-expired").Parse(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{if .EN}}Sign-in expired{{else}}登录已过期{{end}}</title></head>
<body style="font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#faf9f7">
<div style="background:#fff;border:1px solid #e9e5dd;border-radius:16px;padding:2.5rem;max-width:24rem;text-align:center">
<h2>{{if .EN}}This sign-in link has expired{{else}}本次登录已过期{{end}}</h2>
<p style="color:#8a857c">{{if .EN}}Go back to the site you were visiting and sign in again.{{else}}请回到你正在访问的网站，重新登录即可。{{end}}</p>
</div></body></html>`))

// finishCentralSignIn runs after the provider callback verified an identity
// for a login-mode apex state: sign the SSO, then mint the subsite code.
func finishCentralSignIn(w http.ResponseWriter, r *http.Request, st apexState, id fwdIdentity) {
	central := domains.NormalizeHost(inboundHost(r))
	sid := randomToken(16)
	authTime := time.Now().Unix()
	signed, err := signSSOSession(id, central, sid, authTime)
	if err != nil {
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}
	setHostCookie(w, ssoCookieBase, signed, int(ssoLifetime.Seconds()))
	writeLoginHint(w, loginHint{Host: st.Host}) // completed: recovery count back to 0
	code := mintSSOCode(&ssoCode{Host: st.Host, Aud: st.Aud, StateDigest: stateDigest(st.State), SSOSID: sid, AuthTime: authTime, ID: id})
	redirectToSubsite(w, r, st.Host, "/_oauth/session", url.Values{"code": {code}, "state": {st.State}})
}

// ─── Subsite ────────────────────────────────────────────────────────────────

// redirectToCentral sends a subsite sign-in to the central host.
func redirectToCentral(w http.ResponseWriter, r *http.Request, path string, q url.Values) {
	host := domains.NormalizeHost(inboundHost(r))
	q.Set("host", host)
	http.Redirect(w, r, forwardAuthBaseForHost(host)+path+"?"+q.Encode(), http.StatusFound)
}

// handleSSOSession — GET /_oauth/session?code=&state= on the subsite:
// exchange the central host's code for this subsite's own session.
func handleSSOSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	aud := routeAudience(r)
	host := domains.NormalizeHost(inboundHost(r))
	queryState := r.URL.Query().Get("state")
	st, stOK := readSubsiteState(r, queryState)
	c, codeOK := takeSSOCode(r.URL.Query().Get("code"))
	if aud == "" || !codeOK || !stOK ||
		c.Aud != aud || c.Host != host || c.StateDigest != stateDigest(queryState) {
		log.Printf("authservice: code exchange refused on %s (aud=%q code=%v state=%v)", host, aud, codeOK, stOK)
		clearSubsiteState(w, queryState)
		trips := st.Trips
		if !stOK {
			trips = nonceTrips(queryState)
		}
		retryLogin(w, r, trips+1, st.Popup)
		return
	}
	signed, err := signSubsiteSession(c.ID, c.Aud, c.SSOSID, c.AuthTime)
	if err != nil {
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}
	issueFwdSession(w, r, signed, subsiteSessionLifetime)
	clearSubsiteState(w, queryState)
	if st.Popup {
		http.Redirect(w, r, popupDonePath, http.StatusFound)
		return
	}
	http.Redirect(w, r, takePostLoginRedirect(w, r), http.StatusFound)
}

// retryLogin re-enters the login page after a failed exchange (an expired or
// lost state slot, an expired code), or shows an error page once
// maxLoginRoundTrips is exceeded so the browser never loops. The caller
// deletes the failed sign-in's own slot; other sign-ins' slots stay.
func retryLogin(w http.ResponseWriter, r *http.Request, trips int, popup bool) {
	if trips > maxLoginRoundTrips {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		m := loginMessages(acceptLang(r))
		_ = signInErrorTmpl.Execute(w, m)
		return
	}
	q := url.Values{"attempt": {strconv.Itoa(trips)}}
	if popup {
		q.Set("popup", "1")
	}
	http.Redirect(w, r, "/_oauth/login?"+q.Encode(), http.StatusFound)
}

var signInErrorTmpl = template.Must(template.New("signin-error").Parse(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{if .EN}}Sign-in failed{{else}}登录未完成{{end}}</title></head>
<body style="font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#faf9f7">
<div style="background:#fff;border:1px solid #e9e5dd;border-radius:16px;padding:2.5rem;max-width:24rem;text-align:center">
<h2>{{if .EN}}Sign-in could not be completed{{else}}登录未能完成{{end}}</h2>
<p style="color:#8a857c">{{if .EN}}Close other sign-in tabs for this site and try again.{{else}}请关闭本站其他正在登录的标签页后重试。{{end}}</p>
<a href="/_oauth/login?sso=none">{{if .EN}}Try again{{else}}重新登录{{end}}</a>
</div></body></html>`))

// takePostLoginRedirect returns where a completed sign-in goes: the subsite's
// own host-only return URL (same host only), else "/". The cookie is deleted.
func takePostLoginRedirect(w http.ResponseWriter, r *http.Request) string {
	v, ok := hostCookie(r, fwdRedirectCookieBase)
	if !ok {
		return "/"
	}
	setHostCookie(w, fwdRedirectCookieBase, "", -1)
	return sameHostReturnURL(v, inboundHost(r))
}

// sameHostReturnURL accepts a same-host path or an absolute URL on exactly
// the inbound host (vetted by safeReturnURL first); anything else is "/".
func sameHostReturnURL(raw, inbound string) string {
	u, ok := safeReturnURL(raw, inbound)
	if !ok {
		return "/"
	}
	if strings.HasPrefix(u, "/") {
		return u
	}
	if pu, err := url.Parse(u); err == nil && domains.NormalizeHost(pu.Host) == domains.NormalizeHost(inbound) {
		return u
	}
	return "/"
}

// setPostLoginRedirect records the subsite return URL (host-only).
func setPostLoginRedirect(w http.ResponseWriter, value string) {
	setHostCookie(w, fwdRedirectCookieBase, value, int(loginStateLifetime.Seconds()))
}

const popupDonePath = "/_oauth/popup-done"

// handlePopupDone — the landing page of an SDK popup sign-in: tell the opener
// (same origin only) and any same-origin tab, then close.
func handlePopupDone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, popupDonePage)
}

const popupDonePage = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Signed in</title></head>
<body style="font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f5f5f5">
<div style="background:#fff;border-radius:12px;padding:2.5rem 3rem;text-align:center"><h2>&#10003; Signed in</h2><p>You can close this window.</p></div>
<script>
(function(){
  var msg = {type: 'muvee-auth', status: 'signed-in'};
  try { if (window.opener) window.opener.postMessage(msg, location.origin); } catch (e) {}
  try { var bc = new BroadcastChannel('muvee-auth'); bc.postMessage(msg); bc.close(); } catch (e) {}
  setTimeout(function(){ try { window.close(); } catch (e) {} }, 300);
})();
</script>
</body></html>`

// ─── Revocation ─────────────────────────────────────────────────────────────

// revokeSessionIDs records sids revoked by a sign-out with muvee-server.
func revokeSessionIDs(ctx context.Context, sids []string) error {
	if len(sids) == 0 {
		return nil
	}
	body, _ := json.Marshal(map[string][]string{"sids": sids})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, muveeServerURL+"/api/internal/auth/revoke-sids", strings.NewReader(string(body)))
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke-sids returned %d", resp.StatusCode)
	}
	return nil
}

// tokenForRevocation returns the claims of a signature-valid token without
// checking its audience — only used to learn which sids to revoke, which can
// only ever reduce access.
func tokenForRevocation(tokenStr string) *authClaims {
	token, err := jwt.ParseWithClaims(tokenStr, &authClaims{}, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil
	}
	c, _ := token.Claims.(*authClaims)
	return c
}

// fetchSignInTargetByHost resolves a host through muvee-server's by-host
// lookup, including project-less live tunnels. Returns the credential
// audience ("" when the host is not muvee-routed) and the project config
// (nil for a tunnel).
func fetchSignInTargetByHost(ctx context.Context, host string) (*projectAuthConfig, string, error) {
	q := url.Values{"host": {host}, "include_tunnel": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, muveeServerURL+"/api/internal/projects/by-host?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-Muvee-Internal-Key", internalKey)
	resp, err := internalClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("internal project-by-host returned %d", resp.StatusCode)
	}
	var out struct {
		projectAuthConfig
		Aud    string `json:"aud"`
		Tunnel bool   `json:"tunnel"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", err
	}
	if out.Tunnel {
		return nil, out.Aud, nil
	}
	if out.ProjectID == "" || out.Aud != "project:"+out.ProjectID {
		return nil, "", nil
	}
	cfg := out.projectAuthConfig
	return &cfg, out.Aud, nil
}
