package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// Central sign-in state ("f1.<mac>"), BACKLOG-20261117-007 / BUG-20261117-002.
//
// A provider round trip started on the central sign-in host
// (/_oauth/start) keeps everything it needs — target host, the subsite's
// state, mode, device user code, a random nonce — in that host's own
// __Host-fwd_apex_state cookie, which no tenant subdomain can write. The
// provider only carries f1, a MAC over that nonce: fixed length (WeCom caps
// state at 128 bytes) and meaningless without the cookie. The callback accepts
// nothing else for a browser sign-in: the base-domain fwd_oauth_state /
// fwd_oauth_host cookies, the signed host-bound state and the pre-upgrade
// numeric state are gone, because a sibling subdomain could plant all of them
// (login CSRF).
const fwdStatePrefix = "f1."

// apexStateF1 derives the provider state for an apex sign-in nonce.
func apexStateF1(nonce string) string {
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = mac.Write([]byte("muvee-apex-state\x00"))
	_, _ = mac.Write([]byte(nonce))
	return fwdStatePrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:24])
}

// apexStateF1Matches compares a provider state with the one nonce derives.
func apexStateF1Matches(state, nonce string) bool {
	if nonce == "" || !strings.HasPrefix(state, fwdStatePrefix) {
		return false
	}
	return hmac.Equal([]byte(state), []byte(apexStateF1(nonce)))
}
