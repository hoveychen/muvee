package domains

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		baseDomain  string
		baseDomains string
		want        []string
	}{
		{
			name:        "canonical first, csv appended, dedup + lowercase + trim",
			baseDomain:  "muveeai.com",
			baseDomains: "muveeai.com, muvee.ai ,MUVEE.AI",
			want:        []string{"muveeai.com", "muvee.ai"},
		},
		{
			name:        "canonical prepended even when absent from csv",
			baseDomain:  "primary.com",
			baseDomains: "a.com,b.com",
			want:        []string{"primary.com", "a.com", "b.com"},
		},
		{
			name:        "empty canonical uses csv only",
			baseDomain:  "",
			baseDomains: "a.com",
			want:        []string{"a.com"},
		},
		{
			name:        "all empty yields nil",
			baseDomain:  "",
			baseDomains: "",
			want:        nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.baseDomain, tt.baseDomains)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q,%q) = %v, want %v", tt.baseDomain, tt.baseDomains, got, tt.want)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	bases := []string{"muveeai.com", "muvee.ai"}
	tests := []struct {
		host     string
		wantBase string
		wantOK   bool
	}{
		{"app.muveeai.com", "muveeai.com", true},
		{"muveeai.com", "muveeai.com", true},
		{"foo.bar.muvee.ai", "muvee.ai", true},
		{"app.muvee.ai:443", "muvee.ai", true}, // port stripped
		{"APP.MUVEE.AI", "muvee.ai", true},      // case-insensitive
		{"other.com", "", false},
		{"notmuveeai.com", "", false}, // suffix without a dot boundary must not match
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			base, ok := Match(tt.host, bases)
			if base != tt.wantBase || ok != tt.wantOK {
				t.Fatalf("Match(%q) = (%q,%v), want (%q,%v)", tt.host, base, ok, tt.wantBase, tt.wantOK)
			}
		})
	}
}

func TestMatchLongestWins(t *testing.T) {
	// A nested-base configuration: the more specific base must win.
	bases := []string{"ai", "muvee.ai"}
	base, ok := Match("x.muvee.ai", bases)
	if base != "muvee.ai" || !ok {
		t.Fatalf("Match longest = (%q,%v), want (muvee.ai,true)", base, ok)
	}
}
