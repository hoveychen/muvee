package auth

import "testing"

func TestParseTeamIDs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]bool
	}{
		{"empty", "", map[string]bool{}},
		{"single", "T1", map[string]bool{"T1": true}},
		{"multiple with spaces", " T1 , T2 ", map[string]bool{"T1": true, "T2": true}},
		{"ignores empty entries", "T1,,T2,", map[string]bool{"T1": true, "T2": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseTeamIDs(c.raw)
			if len(got) != len(c.want) {
				t.Fatalf("parseTeamIDs(%q) len = %d, want %d (%v)", c.raw, len(got), len(c.want), got)
			}
			for k := range c.want {
				if !got[k] {
					t.Errorf("parseTeamIDs(%q) missing %q (got %v)", c.raw, k, got)
				}
			}
		})
	}
}

func TestTeamAllowed(t *testing.T) {
	cases := []struct {
		name    string
		allowed map[string]bool
		teamID  string
		want    bool
	}{
		{"empty allowlist passes everything", map[string]bool{}, "T1", true},
		{"empty allowlist passes empty team", map[string]bool{}, "", true},
		{"in allowlist", map[string]bool{"T1": true, "T2": true}, "T2", true},
		{"not in allowlist", map[string]bool{"T1": true}, "T9", false},
		{"empty team rejected when restricted", map[string]bool{"T1": true}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := teamAllowed(c.allowed, c.teamID); got != c.want {
				t.Errorf("teamAllowed(%v, %q) = %v, want %v", c.allowed, c.teamID, got, c.want)
			}
		})
	}
}

func TestNewSlackProviderUnconfigured(t *testing.T) {
	t.Setenv("SLACK_CLIENT_ID", "")
	p, err := newSlackProvider("https://example.com/_oauth/slack")
	if err != nil {
		t.Fatalf("newSlackProvider err = %v, want nil", err)
	}
	if p != nil {
		t.Fatalf("newSlackProvider = %v, want nil when SLACK_CLIENT_ID unset", p)
	}
}

func TestSlackProviderMetadata(t *testing.T) {
	p := &slackProvider{}
	if p.Name() != "slack" {
		t.Errorf("Name() = %q, want slack", p.Name())
	}
	if p.DisplayName() != "Slack" {
		t.Errorf("DisplayName() = %q, want Slack", p.DisplayName())
	}
	if p.OrgScoped() != false {
		t.Errorf("OrgScoped() = true, want false")
	}
}
