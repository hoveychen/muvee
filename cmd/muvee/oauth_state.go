package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// stateClaims is the structured payload encoded into the OAuth state query
// parameter. The signed form lets handleOAuthCallback reconstruct the flow
// context (which login-token / mode initiated this round-trip) without
// trusting any state carried in cookies — the device flow already needs to
// work across browsers (CLI vs OAuth window), and the SDK flow needs to work
// across tabs / windows / Tauri shell-open, so cookies cannot be relied on
// for flow correlation.
type stateClaims struct {
	// Mode is the requested completion behaviour. Recognised values:
	//   "login-token" — SDK polled flow (3.2 in the design doc).
	// Legacy callback paths that used a plain random state or the "device-"
	// prefix do not parse here; their handlers continue to compare strings
	// directly.
	Mode string `json:"m"`
	// LoginToken is populated for Mode == "login-token". Opaque to providers;
	// authservice uses it to match the callback to a pending login_token map
	// entry.
	LoginToken string `json:"lt,omitempty"`
	Nonce      string `json:"n"`
	IssuedAt   int64  `json:"iat"`
}

// stateMaxAge bounds how long a signed state remains acceptable. 10 minutes
// covers slow multi-step OAuth screens (Feishu / WeCom consent flows) while
// still tightly bounding replay attempts on a captured callback URL.
const stateMaxAge = 10 * 60

func newStateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// signState renders claims as "<base64-payload>.<base64-hmac>" so the result
// fits into a single OAuth state query parameter. Reuses jwtSecret — the same
// shared secret that already gates session JWT issuance and the internal-key
// derivation — so the deployment surface is one secret, not two.
func signState(c stateClaims) (string, error) {
	if c.Nonce == "" {
		c.Nonce = newStateNonce()
	}
	if c.IssuedAt == 0 {
		c.IssuedAt = time.Now().Unix()
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = mac.Write([]byte(p))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return p + "." + sig, nil
}

// verifyState parses a signed state, checks its HMAC and expiry, and returns
// the claims. Errors are intentionally generic — callers should log them and
// reject the request without echoing details back to the user agent.
func verifyState(s string) (stateClaims, error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return stateClaims{}, errors.New("malformed state")
	}
	p, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = mac.Write([]byte(p))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) != 1 {
		return stateClaims{}, errors.New("invalid state signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return stateClaims{}, err
	}
	var c stateClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return stateClaims{}, err
	}
	if c.IssuedAt+stateMaxAge < time.Now().Unix() {
		return c, errors.New("state expired")
	}
	return c, nil
}

// Forward-auth login state ("f1.<payload>.<mac>").
//
// The browser login started on /_oauth/login?provider=X used to send a plain
// time.Now().UnixNano() as the OAuth state: guessable, and carrying nothing
// about where the login began. The callback lands on the forwardAuthBase host
// and reads the post-login return URL from fwd_oauth_redirect — a base-domain
// cookie that any sibling project subdomain can overwrite — so a tenant could
// toss its own host in there and receive users who were signing in elsewhere.
//
// The fwd state binds the flow to the host it started on: the callback only
// follows a return URL on that host (boundPostLoginRedirect). The state holds
// an 8-byte hash of the host rather than the host itself, and the host travels
// in the fwd_oauth_host cookie (checked against the hash): some providers cap
// state length (WeCom: 128 bytes) and project hosts have no length limit, so
// the state is a fixed 58 bytes. The HMAC reuses jwtSecret with a
// domain-separation label so it can never be confused with a signState token.
const (
	fwdStatePrefix    = "f1."
	fwdHostCookieName = "fwd_oauth_host"
)

func fwdStateMAC(p string) []byte {
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = mac.Write([]byte("muvee-fwd-state\x00"))
	_, _ = mac.Write([]byte(p))
	return mac.Sum(nil)[:16]
}

func fwdHostHash(host string) []byte {
	h := sha256.Sum256([]byte("muvee-fwd-host\x00" + host))
	return h[:8]
}

func signFwdState(host string) string { return signFwdStateAt(host, time.Now()) }

func signFwdStateAt(host string, at time.Time) string {
	payload := make([]byte, 24)
	if _, err := rand.Read(payload[:12]); err != nil {
		panic(err)
	}
	binary.BigEndian.PutUint32(payload[12:16], uint32(at.Unix()))
	copy(payload[16:], fwdHostHash(host))
	p := base64.RawURLEncoding.EncodeToString(payload)
	return fwdStatePrefix + p + "." + base64.RawURLEncoding.EncodeToString(fwdStateMAC(p))
}

// isFwdState reports whether s claims to be a fwd state (valid or not).
func isFwdState(s string) bool { return strings.HasPrefix(s, fwdStatePrefix) }

// verifyFwdState checks a fwd state's signature and age and that it was
// minted for host (the value of the fwd_oauth_host cookie).
func verifyFwdState(s, host string) bool {
	if !isFwdState(s) || host == "" {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(s, fwdStatePrefix), ".", 2)
	if len(parts) != 2 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, fwdStateMAC(parts[0])) {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) != 24 {
		return false
	}
	iat := int64(binary.BigEndian.Uint32(payload[12:16]))
	now := time.Now().Unix()
	if iat+stateMaxAge < now || iat > now+60 {
		return false
	}
	return hmac.Equal(payload[16:], fwdHostHash(host))
}

// isFreshLegacyState accepts the pre-v1.40.4 unsigned state — the decimal
// UnixNano at which the login started — only while it is younger than
// stateMaxAge. That covers logins in flight across the upgrade (and replicas
// still on the old version) without depending on process start time; any
// other unsigned value is rejected so nobody can opt out of the host binding.
func isFreshLegacyState(s string) bool {
	if len(s) < 18 || len(s) > 20 {
		return false
	}
	ns, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ns <= 0 {
		return false
	}
	age := time.Since(time.Unix(0, ns))
	return age >= -time.Minute && age <= stateMaxAge*time.Second
}
