// Package sms provides phone-number normalization and a pluggable SMS sender
// used by the self-service phone / one-time-code login on the downstream
// ForwardAuth sign-in page.
package sms

import (
	"errors"
	"regexp"
	"strings"
)

// cnMobileRe matches a bare Chinese mobile number (11 digits, 1[3-9] prefix).
var cnMobileRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// ErrInvalidPhone is returned when a phone number cannot be normalized.
var ErrInvalidPhone = errors.New("invalid phone number")

// NormalizePhone canonicalizes a user-entered phone number to E.164
// (e.g. "+8613800138000"). Bare digits default to China (+86). Numbers already
// in international form (leading "+") are validated and kept; a "+86" number is
// held to the Chinese mobile shape. Separators (spaces, dashes, parens) are
// stripped. Returns ErrInvalidPhone on anything that doesn't fit.
func NormalizePhone(raw string) (string, error) {
	s := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')':
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	if s == "" {
		return "", ErrInvalidPhone
	}

	if strings.HasPrefix(s, "+") {
		digits := s[1:]
		if !isDigits(digits) || len(digits) < 8 || len(digits) > 15 {
			return "", ErrInvalidPhone
		}
		if strings.HasPrefix(digits, "86") {
			local := digits[2:]
			if !cnMobileRe.MatchString(local) {
				return "", ErrInvalidPhone
			}
			return "+86" + local, nil
		}
		return "+" + digits, nil
	}

	// Bare digits: treat as a Chinese number, tolerating a 0086 / 86 prefix.
	s = strings.TrimPrefix(s, "0086")
	if len(s) == 13 && strings.HasPrefix(s, "86") {
		s = s[2:]
	}
	if !cnMobileRe.MatchString(s) {
		return "", ErrInvalidPhone
	}
	return "+86" + s, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
