package main

import (
	"encoding/base64"
	"testing"
)

// entraDataURI builds the exact avatar string shape the Entra provider hands to
// signForwardJWT: "data:<mime>;base64," + base64(photo bytes). See
// internal/auth/entra.go fetchAvatar — Entra ID tokens carry no `picture`
// claim, so the Graph photo bytes are inlined instead of referenced by URL.
func entraDataURI(photoBytes int) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(make([]byte, photoBytes))
}

// browserCookieLimit is the per-cookie byte budget every major browser
// enforces on name=value (RFC 6265 §6.1 floor; Chrome/Firefox/Safari all cap
// at 4096). A Set-Cookie beyond it is dropped silently — no error reaches the
// page — which for muvee_fwd_session means the freshly signed session never
// sticks and the project subdomain bounces the user straight back to login.
const browserCookieLimit = 4096

// cookieBytes returns what the browser actually measures for the session
// cookie: the name, the "=", and the signed JWT.
func cookieBytes(signed string) int {
	return len("muvee_fwd_session") + 1 + len(signed)
}

func withTestJWTSecret(t *testing.T) {
	t.Helper()
	prev := jwtSecret
	jwtSecret = []byte("test-secret-that-is-long-enough-for-hs256")
	t.Cleanup(func() { jwtSecret = prev })
}

// TestForwardSessionCookieFitsWithEntraAvatar pins the actual bug: an Entra
// avatar is a base64 data URI of the Graph photo, and entra.go's own comment
// puts 96x96 JPEGs at 2-8 KB. Base64 inflates that by 4/3, then the JWT
// payload base64 inflates it again by 4/3, so anything above a ~2 KB photo
// pushes muvee_fwd_session past the 4096-byte cookie limit and the session is
// silently dropped by the browser.
func TestForwardSessionCookieFitsWithEntraAvatar(t *testing.T) {
	withTestJWTSecret(t)

	// 4 KB and 8 KB sit squarely inside the size range entra.go documents, and
	// 256 KB is the cap fetchAvatar itself allows through.
	for _, photoBytes := range []int{4 << 10, 8 << 10, 256 << 10} {
		signed, err := signForwardJWT(
			"3f9b1c62-0000-4000-8000-000000000001",
			"user@contoso.com",
			"Contoso User",
			entraDataURI(photoBytes),
			"entra",
		)
		if err != nil {
			t.Fatalf("sign (%d byte photo): %v", photoBytes, err)
		}
		if got := cookieBytes(signed); got > browserCookieLimit {
			t.Errorf("%d byte Entra photo produced a %d byte session cookie, over the %d byte browser limit: the browser drops it and the subdomain login loops",
				photoBytes, got, browserCookieLimit)
		}
	}
}

// TestForwardProjectSessionCookieFitsWithEntraAvatar covers the other signer.
// A password / phone project session carries whatever avatar the identity
// already has on file, which for a user who first arrived through Entra is the
// same oversized data URI.
func TestForwardProjectSessionCookieFitsWithEntraAvatar(t *testing.T) {
	withTestJWTSecret(t)

	signed, err := signForwardProjectJWT(
		"user@contoso.com",
		"Contoso User",
		entraDataURI(8<<10),
		"password",
		"3f9b1c62-0000-4000-8000-000000000002",
	)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got := cookieBytes(signed); got > browserCookieLimit {
		t.Errorf("project session cookie is %d bytes, over the %d byte browser limit", got, browserCookieLimit)
	}
}

// TestForwardSessionKeepsNormalAvatarURL is the regression guard on the fix:
// Google's `picture` and Lark's `avatar_url` are ordinary https URLs of a few
// hundred bytes, and they must still round-trip into the session claims. A
// size guard that also strips those would break avatars for every provider
// instead of just protecting the cookie.
func TestForwardSessionKeepsNormalAvatarURL(t *testing.T) {
	withTestJWTSecret(t)

	const avatar = "https://lh3.googleusercontent.com/a/ACg8ocKQ0000000000000000000000000000000000000000000000=s96-c"
	signed, err := signForwardJWT(
		"3f9b1c62-0000-4000-8000-000000000003",
		"user@example.com",
		"Example User",
		avatar,
		"google",
	)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := parseForwardJWT(signed)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.AvatarURL != avatar {
		t.Errorf("avatar_url = %q, want the original URL preserved", claims.AvatarURL)
	}
}
