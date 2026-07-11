package api

import (
	"strings"
	"testing"
)

// ─── validateAliasHost ──────────────────────────────────────────────────────

func TestValidateAliasHost(t *testing.T) {
	const base = "domain.com"
	cases := []struct {
		name    string
		host    string
		wantErr bool
		errHas  string
	}{
		// The feature under test: a second prefix under the platform base domain.
		{name: "base-domain second prefix", host: "two.domain.com", wantErr: false},
		{name: "base-domain second prefix uppercased", host: "TWO.Domain.Com", wantErr: false},
		// External custom domains keep working unchanged.
		{name: "external custom domain", host: "app.othersite.com", wantErr: false},
		// Multi-label under base never collides with the single-label prefix namespace.
		{name: "multi-label under base", host: "a.b.domain.com", wantErr: false},
		// Rejections.
		{name: "apex of base domain", host: "domain.com", wantErr: true, errHas: "base domain"},
		{name: "reserved prefix under base", host: "registry.domain.com", wantErr: true, errHas: "reserved"},
		{name: "empty", host: "", wantErr: true},
		{name: "no dot", host: "foo", wantErr: true},
		{name: "leading hyphen label", host: "-bad.domain.com", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAliasHost(tc.host, base)
			if tc.wantErr && err == nil {
				t.Fatalf("validateAliasHost(%q) = nil, want error", tc.host)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateAliasHost(%q) = %v, want nil", tc.host, err)
			}
			if tc.errHas != "" && (err == nil || !strings.Contains(err.Error(), tc.errHas)) {
				t.Fatalf("validateAliasHost(%q) error = %v, want it to contain %q", tc.host, err, tc.errHas)
			}
		})
	}
}

// ─── baseSubPrefix ──────────────────────────────────────────────────────────

func TestBaseSubPrefix(t *testing.T) {
	const base = "domain.com"
	cases := []struct {
		host      string
		wantLabel string
		wantOK    bool
	}{
		{host: "two.domain.com", wantLabel: "two", wantOK: true},
		{host: "TWO.domain.com", wantLabel: "two", wantOK: true},
		{host: "a.b.domain.com", wantLabel: "", wantOK: false},
		{host: "app.othersite.com", wantLabel: "", wantOK: false},
		{host: "domain.com", wantLabel: "", wantOK: false},
	}
	for _, tc := range cases {
		label, ok := baseSubPrefix(tc.host, base)
		if ok != tc.wantOK || label != tc.wantLabel {
			t.Errorf("baseSubPrefix(%q) = (%q, %v), want (%q, %v)", tc.host, label, ok, tc.wantLabel, tc.wantOK)
		}
	}
}
