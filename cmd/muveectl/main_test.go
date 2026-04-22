package main

import "testing"

func TestResolveCreds(t *testing.T) {
	cases := []struct {
		name           string
		cfg            Config
		serverOverride string
		tokenOverride  string
		env            map[string]string
		wantServer     string
		wantToken      string
	}{
		{
			name:       "config only",
			cfg:        Config{Server: "https://cfg/", Token: "cfg-token"},
			wantServer: "https://cfg",
			wantToken:  "cfg-token",
		},
		{
			name:       "env overrides config",
			cfg:        Config{Server: "https://cfg", Token: "cfg-token"},
			env:        map[string]string{envServer: "https://env", envToken: "env-token"},
			wantServer: "https://env",
			wantToken:  "env-token",
		},
		{
			name:           "flag overrides env and config",
			cfg:            Config{Server: "https://cfg", Token: "cfg-token"},
			env:            map[string]string{envServer: "https://env", envToken: "env-token"},
			serverOverride: "https://flag",
			tokenOverride:  "flag-token",
			wantServer:     "https://flag",
			wantToken:      "flag-token",
		},
		{
			name:           "partial flag only overrides that field",
			cfg:            Config{Server: "https://cfg", Token: "cfg-token"},
			env:            map[string]string{envServer: "https://env", envToken: "env-token"},
			tokenOverride:  "flag-token",
			wantServer:     "https://env", // env still wins for server
			wantToken:      "flag-token",
		},
		{
			name:       "trailing slash stripped from server",
			cfg:        Config{Server: "https://cfg///"},
			wantServer: "https://cfg",
			wantToken:  "",
		},
		{
			name:       "empty env value does not override config",
			cfg:        Config{Server: "https://cfg", Token: "cfg-token"},
			env:        map[string]string{envServer: "", envToken: ""},
			wantServer: "https://cfg",
			wantToken:  "cfg-token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if tc.env == nil {
					return ""
				}
				return tc.env[k]
			}
			gotServer, gotToken := resolveCreds(&tc.cfg, tc.serverOverride, tc.tokenOverride, getenv)
			if gotServer != tc.wantServer {
				t.Errorf("server = %q, want %q", gotServer, tc.wantServer)
			}
			if gotToken != tc.wantToken {
				t.Errorf("token = %q, want %q", gotToken, tc.wantToken)
			}
		})
	}
}

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
