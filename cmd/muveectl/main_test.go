package main

import (
	"encoding/json"
	"testing"
)

// singleProfileConfig builds a Config with one profile named "default" set as
// the current profile — the post-migration equivalent of the legacy
// {server, token} shape, used by tests that don't care about multi-profile.
func singleProfileConfig(server, token string) Config {
	return Config{
		CurrentProfile: defaultProfile,
		Profiles:       map[string]Profile{defaultProfile: {Server: server, Token: token}},
	}
}

func TestResolveCreds(t *testing.T) {
	cases := []struct {
		name            string
		cfg             Config
		profileOverride string
		serverOverride  string
		tokenOverride   string
		env             map[string]string
		wantServer      string
		wantToken       string
	}{
		{
			name:       "config only",
			cfg:        singleProfileConfig("https://cfg/", "cfg-token"),
			wantServer: "https://cfg",
			wantToken:  "cfg-token",
		},
		{
			name:       "env overrides config",
			cfg:        singleProfileConfig("https://cfg", "cfg-token"),
			env:        map[string]string{envServer: "https://env", envToken: "env-token"},
			wantServer: "https://env",
			wantToken:  "env-token",
		},
		{
			name:           "flag overrides env and config",
			cfg:            singleProfileConfig("https://cfg", "cfg-token"),
			env:            map[string]string{envServer: "https://env", envToken: "env-token"},
			serverOverride: "https://flag",
			tokenOverride:  "flag-token",
			wantServer:     "https://flag",
			wantToken:      "flag-token",
		},
		{
			name:          "partial flag only overrides that field",
			cfg:           singleProfileConfig("https://cfg", "cfg-token"),
			env:           map[string]string{envServer: "https://env", envToken: "env-token"},
			tokenOverride: "flag-token",
			wantServer:    "https://env", // env still wins for server
			wantToken:     "flag-token",
		},
		{
			name:       "trailing slash stripped from server",
			cfg:        singleProfileConfig("https://cfg///", ""),
			wantServer: "https://cfg",
			wantToken:  "",
		},
		{
			name:       "empty env value does not override config",
			cfg:        singleProfileConfig("https://cfg", "cfg-token"),
			env:        map[string]string{envServer: "", envToken: ""},
			wantServer: "https://cfg",
			wantToken:  "cfg-token",
		},
		{
			name: "profile flag picks a non-current profile",
			cfg: Config{
				CurrentProfile: "dev",
				Profiles: map[string]Profile{
					"dev":  {Server: "https://dev", Token: "dev-tok"},
					"prod": {Server: "https://prod", Token: "prod-tok"},
				},
			},
			profileOverride: "prod",
			wantServer:      "https://prod",
			wantToken:       "prod-tok",
		},
		{
			name: "MUVEECTL_PROFILE env picks profile when no flag",
			cfg: Config{
				CurrentProfile: "dev",
				Profiles: map[string]Profile{
					"dev":  {Server: "https://dev", Token: "dev-tok"},
					"prod": {Server: "https://prod", Token: "prod-tok"},
				},
			},
			env:        map[string]string{envProfile: "prod"},
			wantServer: "https://prod",
			wantToken:  "prod-tok",
		},
		{
			name: "profile flag beats MUVEECTL_PROFILE env",
			cfg: Config{
				CurrentProfile: "dev",
				Profiles: map[string]Profile{
					"dev":   {Server: "https://dev", Token: "dev-tok"},
					"prod":  {Server: "https://prod", Token: "prod-tok"},
					"stage": {Server: "https://stage", Token: "stage-tok"},
				},
			},
			env:             map[string]string{envProfile: "prod"},
			profileOverride: "stage",
			wantServer:      "https://stage",
			wantToken:       "stage-tok",
		},
		{
			name:            "unknown profile yields empty creds (flags can still fill in)",
			cfg:             singleProfileConfig("https://cfg", "cfg-token"),
			profileOverride: "ghost",
			serverOverride:  "https://flag",
			tokenOverride:   "flag-token",
			wantServer:      "https://flag",
			wantToken:       "flag-token",
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
			gotServer, gotToken := resolveCreds(&tc.cfg, tc.profileOverride, tc.serverOverride, tc.tokenOverride, getenv)
			if gotServer != tc.wantServer {
				t.Errorf("server = %q, want %q", gotServer, tc.wantServer)
			}
			if gotToken != tc.wantToken {
				t.Errorf("token = %q, want %q", gotToken, tc.wantToken)
			}
		})
	}
}

func TestParseConfigMigratesLegacyShape(t *testing.T) {
	legacy := []byte(`{"server":"https://old.example.com","token":"old-token"}`)
	c := parseConfig(legacy)
	if c.CurrentProfile != defaultProfile {
		t.Fatalf("CurrentProfile = %q, want %q", c.CurrentProfile, defaultProfile)
	}
	p, ok := c.Profiles[defaultProfile]
	if !ok {
		t.Fatalf("default profile not created; profiles = %#v", c.Profiles)
	}
	if p.Server != "https://old.example.com" || p.Token != "old-token" {
		t.Fatalf("migrated profile = %#v, want server=https://old.example.com token=old-token", p)
	}
	// Re-saving must produce the new shape — no top-level server/token keys.
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(out, &roundtrip); err != nil {
		t.Fatalf("unmarshal roundtrip: %v", err)
	}
	if _, has := roundtrip["server"]; has {
		t.Errorf("roundtrip retained legacy top-level 'server' key: %s", string(out))
	}
	if _, has := roundtrip["token"]; has {
		t.Errorf("roundtrip retained legacy top-level 'token' key: %s", string(out))
	}
}

func TestParseConfigKeepsMultiProfile(t *testing.T) {
	raw := []byte(`{
		"current_profile": "prod",
		"profiles": {
			"dev":  {"server": "https://dev",  "token": "dev-tok"},
			"prod": {"server": "https://prod", "token": "prod-tok"}
		}
	}`)
	c := parseConfig(raw)
	if c.CurrentProfile != "prod" {
		t.Errorf("CurrentProfile = %q, want prod", c.CurrentProfile)
	}
	if len(c.Profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(c.Profiles))
	}
	if c.Profiles["dev"].Token != "dev-tok" || c.Profiles["prod"].Server != "https://prod" {
		t.Errorf("profiles content wrong: %#v", c.Profiles)
	}
}

func TestParseConfigEmpty(t *testing.T) {
	c := parseConfig([]byte(`{}`))
	if c.CurrentProfile != "" {
		t.Errorf("CurrentProfile = %q, want empty", c.CurrentProfile)
	}
	if c.Profiles == nil {
		t.Errorf("Profiles map should be non-nil to avoid nil-write panics")
	}
}

func TestResolveProfilePrecedence(t *testing.T) {
	cfg := &Config{CurrentProfile: "cfg-active"}
	cases := []struct {
		name     string
		flag     string
		env      string
		want     string
	}{
		{"flag wins", "flag-name", "env-name", "flag-name"},
		{"env wins over config", "", "env-name", "env-name"},
		{"config fallback", "", "", "cfg-active"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == envProfile {
					return tc.env
				}
				return ""
			}
			if got := resolveProfile(cfg, tc.flag, getenv); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShouldShowSkillOutdatedNotice(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		embedded  string
		wantShow  bool
	}{
		{"embedded newer should notify", "1", "2", true},
		{"embedded older should not notify", "3", "2", false},
		{"same version should not notify", "2", "2", false},
		{"semver upgrade beyond 9", "1.0.9", "1.0.10", true},
		{"semver downgrade beyond 9", "1.0.10", "1.0.9", false},
		{"semver major bump", "1.99.99", "2.0.0", true},
		{"empty embedded should not notify", "1", "", false},
		{"empty installed with embedded should notify", "", "1", true},
		{"unparseable but equal should not notify", "alpha", "alpha", false},
		{"unparseable and different should notify", "alpha", "beta", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldShowSkillOutdatedNotice(tc.installed, tc.embedded)
			if got != tc.wantShow {
				t.Errorf("shouldShowSkillOutdatedNotice(%q, %q) = %v, want %v", tc.installed, tc.embedded, got, tc.wantShow)
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
