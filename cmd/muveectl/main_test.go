package main

import "testing"

func TestShouldShowUpdateNotice(t *testing.T) {
	cases := []struct {
		name          string
		cachedLatest  string
		current       string
		wantShow      bool
	}{
		{"downgrade should not notify", "v1.11.4", "v1.11.6", false},
		{"upgrade should notify", "v1.11.6", "v1.11.4", true},
		{"same version should not notify", "v1.11.6", "v1.11.6", false},
		{"numeric not lexicographic", "v1.11.9", "v1.11.10", false},
		{"numeric upgrade beyond 9", "v1.11.10", "v1.11.9", true},
		{"major bump", "v2.0.0", "v1.99.99", true},
		{"dev current should not notify", "v1.11.6", "dev", false},
		{"empty cache should not notify", "", "v1.11.6", false},
		{"unparseable remote should not notify", "latest", "v1.11.6", false},
		{"unparseable current should not notify", "v1.11.6", "custom-build", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldShowUpdateNotice(tc.cachedLatest, tc.current)
			if got != tc.wantShow {
				t.Errorf("shouldShowUpdateNotice(%q, %q) = %v, want %v", tc.cachedLatest, tc.current, got, tc.wantShow)
			}
		})
	}
}
