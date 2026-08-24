package main

import (
	"net/http/httptest"
	"testing"
)

// A forward-auth response header carrying a non-ASCII value takes down the
// upstream, not just the header.
//
// Observed 2026-08-24 on fleet-cloud.muveeai.com: every request 502'd *after* a
// successful Feishu login. The project container was healthy (direct hits on
// its published port returned 200, including from inside the Traefik
// container), and Traefik's route pointed at the right address — but the access
// log showed `OriginStatus: 502` with an 11-byte body ("Bad Gateway"). The only
// structural difference from a working project was this forward-auth
// middleware, which copies `X-Forwarded-User-Name` onto the upstream request.
// That name is the Feishu display name: Chinese characters.
//
// Isolated against the same backend and path: no header → 200; ASCII value →
// 200; any value containing a byte > 0x7F → the connection is dropped with no
// response at all, and nothing reaches the application's log. The upstream was
// `tiny_http`, whose header parser is ASCII-only by contract
// (`tiny_http-0.12.0/src/common.rs:172`: `AsciiString::from_ascii(value)`), so
// the request dies before any application code runs. RFC 9110 agrees: field
// values are ASCII; non-ASCII is deprecated obs-text that a server may reject.
//
// So the encoding has to happen here, on the way out. Two properties matter:
//   - a value that is already ASCII must pass through byte-identical, so
//     nothing that works today changes;
//   - a non-ASCII value must survive in a decodable form rather than killing
//     the request.
func TestSetUserHeaders_KeepsASCIIValuesVerbatim(t *testing.T) {
	rec := httptest.NewRecorder()
	setUserHeaders(rec, &authClaims{
		Email:     "boss@example.com",
		UserID:    "u-1",
		Name:      "Hovey Chen",
		AvatarURL: "https://example.com/a.png",
		Provider:  "feishu",
	})
	for header, want := range map[string]string{
		"X-Forwarded-User":          "boss@example.com",
		"X-Forwarded-User-Id":       "u-1",
		"X-Forwarded-User-Name":     "Hovey Chen",
		"X-Forwarded-User-Avatar":   "https://example.com/a.png",
		"X-Forwarded-User-Provider": "feishu",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want the value verbatim (%q)", header, got, want)
		}
	}
}

func TestSetUserHeaders_EncodesNonASCII(t *testing.T) {
	rec := httptest.NewRecorder()
	setUserHeaders(rec, &authClaims{
		Email:  "boss@example.com",
		UserID: "u-1",
		Name:   "陈昊维",
	})
	got := rec.Header().Get("X-Forwarded-User-Name")
	if got != "%E9%99%88%E6%98%8A%E7%BB%B4" {
		t.Errorf("X-Forwarded-User-Name = %q, want percent-encoded UTF-8", got)
	}
	for _, b := range []byte(got) {
		if b > 0x7f {
			t.Fatalf("header value still carries a non-ASCII byte %#x: %q", b, got)
		}
	}
}

// A literal `%` in an otherwise-ASCII value would make the encoded form
// ambiguous ("50%" vs a real escape), so it is escaped too — but only when the
// value needs encoding at all, which keeps the ASCII path verbatim.
func TestSetUserHeaders_PercentIsUnambiguous(t *testing.T) {
	ascii := httptest.NewRecorder()
	setUserHeaders(ascii, &authClaims{UserID: "u", Name: "50% off"})
	if got := ascii.Header().Get("X-Forwarded-User-Name"); got != "50% off" {
		t.Errorf("an ASCII value with %% must stay verbatim, got %q", got)
	}

	mixed := httptest.NewRecorder()
	setUserHeaders(mixed, &authClaims{UserID: "u", Name: "50% 昊"})
	if got := mixed.Header().Get("X-Forwarded-User-Name"); got != "50%25 %E6%98%8A" {
		t.Errorf("mixed value = %q, want the %% escaped alongside the CJK", got)
	}
}

// Every header this function writes goes through the same guard: an avatar URL
// with a non-ASCII path, or a provider label in Chinese, would break the
// upstream exactly the same way.
func TestSetUserHeaders_GuardsEveryHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	setUserHeaders(rec, &authClaims{
		Email:     "陈@example.com",
		UserID:    "用户-1",
		Name:      "昊",
		AvatarURL: "https://example.com/头像.png",
		Provider:  "飞书",
	})
	for header, value := range rec.Header() {
		for _, b := range []byte(value[0]) {
			if b > 0x7f {
				t.Errorf("%s leaks a non-ASCII byte %#x: %q", header, b, value[0])
				break
			}
		}
	}
}
