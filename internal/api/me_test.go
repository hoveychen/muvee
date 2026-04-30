package api

import (
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestValidateProfileUpdate(t *testing.T) {
	long := strings.Repeat("x", 101)
	cases := []struct {
		desc   string
		name   *string
		avatar *string
		ok     bool
	}{
		{"both nil leaves user untouched", nil, nil, true},
		{"valid name", strPtr("Yuheng"), nil, true},
		{"name with surrounding spaces is trimmed before length check", strPtr("  Yuheng  "), nil, true},
		{"empty name rejected", strPtr(""), nil, false},
		{"whitespace-only name rejected", strPtr("   "), nil, false},
		{"too-long name rejected", strPtr(long), nil, false},
		{"empty avatar means clear", nil, strPtr(""), true},
		{"https avatar accepted", nil, strPtr("https://example.com/a.png"), true},
		{"http avatar accepted", nil, strPtr("http://example.com/a.png"), true},
		{"non-url avatar rejected", nil, strPtr("just-a-string"), false},
		{"javascript scheme rejected", nil, strPtr("javascript:alert(1)"), false},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			err := validateProfileUpdate(tc.name, tc.avatar)
			gotOK := err == nil
			if gotOK != tc.ok {
				t.Errorf("validateProfileUpdate(%v,%v) = %v, want ok=%v", tc.name, tc.avatar, err, tc.ok)
			}
		})
	}
}
