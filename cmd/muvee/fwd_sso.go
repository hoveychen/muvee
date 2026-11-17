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
	// post-login return URL cookie.
	loginStateLifetime = 10 * time.Minute
	// maxLoginRoundTrips: failed code exchanges re-enter the login page with a
	// counter; past this many the user gets an error page instead of a loop.
	maxLoginRoundTrips = 2
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
)

// legacyFwdCookies are the base-domain cookies earlier releases set. They are
// never written any more; sign-out, a rejected session and a completed
// sign-in delete them (Domain=<base>) so they stop riding along to tenants.
var legacyFwdCookies = []string{
	"muvee_fwd_session", "fwd_oauth_state", "fwd_oauth_host",
	"muvee_invite_token", "muvee_fwd_signed_out", "fwd_oauth_redirect",
}

// fwdAllCookieNames lists every auth cookie name, new and legacy, that must
// never reach a tenant: the HTTP provider passes it to the embed-bridge
// strip middleware explicitly (see internal/api traefik generation).
func fwdAllCookieNames() []string {
	out := []string{}
	for _, b := range []string{fwdSessionCookieBase, ssoCookieBase, fwdStateCookieBase, apexStateCookieBase, signedOutCookieBase, fwdRedirectCookieBase} {
		out = append(out, "__Host-"+b)
	}
	return append(out, legacyFwdCookies...)
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
	i := strings.IndexByte(s, '.')
	if i <= 0 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s[:i])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(s[i+1:])
	if err != nil || !hmac.Equal(sig, sealMAC(purpose, b)) {
		return false
	}
	var env struct {
		T int64           `json:"t"`
		V json.RawMessage `json:"v"`
	}
	if json.Unmarshal(b, &env) != nil {
		return false
	}
	age := time.Since(time.Unix(env.T, 0))
	if age < -time.Minute || age > maxAge {
		return false
	}
	return json.Unmarshal(env.V, v) == nil
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

func writeSubsiteState(w http.ResponseWriter, r *http.Request, trips int, popup bool) string {
	st := subsiteState{Host: domains.NormalizeHost(inboundHost(r)), Nonce: randomToken(18), Trips: trips, Popup: popup}
	setHostCookie(w, fwdStateCookieBase, sealValue("fwd-state", st), int(loginStateLifetime.Seconds()))
	return st.Nonce
}

func readSubsiteState(r *http.Request) (subsiteState, bool) {
	var st subsiteState
	v, ok := hostCookie(r, fwdStateCookieBase)
	if !ok || !openValue("fwd-state", v, loginStateLifetime, &st) {
		return st, false
	}
	return st, st.Host == domains.NormalizeHost(inboundHost(r))
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

func routeAudience(r *http.Request) string {
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
	setHostCookie(w, apexStateCookieBase, sealValue("apex-state", st), int(loginStateLifetime.Seconds()))
	http.Redirect(w, r, p.AuthCodeURL(apexStateF1(st.Nonce), oauthRedirectForHost(central, providerName)), http.StatusFound)
}

// readApexState returns the central sign-in context matching the provider's
// state, deleting the cookie (single use).
func readApexState(w http.ResponseWriter, r *http.Request, queryState string) (apexState, bool) {
	var st apexState
	v, ok := hostCookie(r, apexStateCookieBase)
	if !ok {
		return st, false
	}
	setHostCookie(w, apexStateCookieBase, "", -1)
	if !openValue("apex-state", v, loginStateLifetime, &st) || !apexStateF1Matches(queryState, st.Nonce) {
		return apexState{}, false
	}
	return st, true
}

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
	st, stOK := readSubsiteState(r)
	queryState := r.URL.Query().Get("state")
	c, codeOK := takeSSOCode(r.URL.Query().Get("code"))
	if aud == "" || !codeOK || !stOK || queryState == "" ||
		!hmac.Equal([]byte(queryState), []byte(st.Nonce)) ||
		c.Aud != aud || c.Host != host || c.StateDigest != stateDigest(queryState) {
		log.Printf("authservice: code exchange refused on %s (aud=%q code=%v state=%v)", host, aud, codeOK, stOK)
		retryLogin(w, r, st.Trips+1, st.Popup)
		return
	}
	signed, err := signSubsiteSession(c.ID, c.Aud, c.SSOSID, c.AuthTime)
	if err != nil {
		http.Error(w, "sign error", http.StatusInternalServerError)
		return
	}
	issueFwdSession(w, r, signed, subsiteSessionLifetime)
	setHostCookie(w, fwdStateCookieBase, "", -1)
	if st.Popup {
		http.Redirect(w, r, popupDonePath, http.StatusFound)
		return
	}
	http.Redirect(w, r, takePostLoginRedirect(w, r), http.StatusFound)
}

// retryLogin re-enters the login page after a failed exchange (two tabs
// racing for the state cookie, an expired code), or shows an error page once
// maxLoginRoundTrips is exceeded so the browser never loops.
func retryLogin(w http.ResponseWriter, r *http.Request, trips int, popup bool) {
	setHostCookie(w, fwdStateCookieBase, "", -1)
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
