package api

import (
	"testing"

	"github.com/hoveychen/muvee/internal/auth"
)

// TestMergeDownstreamProviders_AppendsDBEnabledSocialProviders pins the
// bug: enabling Google via /admin/settings while the platform server has no
// GOOGLE_CLIENT_ID env var must surface Google in the project Auth tab's
// SIGN-IN PROVIDERS list — so muvee-authservice (which uses the merged set
// for the downstream /_oauth/login page) and the project owner agree on
// what's on offer.
func TestMergeDownstreamProviders_AppendsDBEnabledSocialProviders(t *testing.T) {
	env := []auth.ProviderInfo{
		{ID: "feishu", DisplayName: "飞书 / Lark"},
	}
	cfg := auth.SocialConfigs{
		Google: &auth.GoogleConfig{ClientID: "x", ClientSecret: "y"},
	}

	got := mergeDownstreamProviders(env, cfg)

	ids := make(map[string]string, len(got))
	for _, p := range got {
		ids[p.ID] = p.DisplayName
	}
	if _, ok := ids["feishu"]; !ok {
		t.Errorf("expected feishu (env) to remain in merged list, got %#v", got)
	}
	if name, ok := ids["google"]; !ok {
		t.Errorf("expected google (DB-enabled) to appear in merged list, got %#v", got)
	} else if name != "Google" {
		t.Errorf("expected google display name 'Google', got %q", name)
	}
}

// TestMergeDownstreamProviders_EnvWinsOnConflict guards the case where the
// admin enables Google in /admin/settings while the platform server ALSO
// has GOOGLE_CLIENT_ID set: the merged list must contain Google exactly
// once, with the env-side entry's identity (no duplicate buttons).
func TestMergeDownstreamProviders_EnvWinsOnConflict(t *testing.T) {
	env := []auth.ProviderInfo{
		{ID: "google", DisplayName: "Google"},
		{ID: "feishu", DisplayName: "飞书 / Lark"},
	}
	cfg := auth.SocialConfigs{
		Google: &auth.GoogleConfig{ClientID: "x", ClientSecret: "y"},
	}

	got := mergeDownstreamProviders(env, cfg)

	count := 0
	for _, p := range got {
		if p.ID == "google" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one google entry in merged list, got %d (%#v)", count, got)
	}
	if len(got) != 2 {
		t.Errorf("expected merged list of length 2, got %d (%#v)", len(got), got)
	}
}

// TestMergeDownstreamProviders_DisabledFlagSkipped guards that an empty
// SocialConfigs (no provider enabled) leaves the env list untouched.
func TestMergeDownstreamProviders_DisabledFlagSkipped(t *testing.T) {
	env := []auth.ProviderInfo{
		{ID: "feishu", DisplayName: "飞书 / Lark"},
	}
	got := mergeDownstreamProviders(env, auth.SocialConfigs{})
	if len(got) != 1 || got[0].ID != "feishu" {
		t.Errorf("expected env list passthrough, got %#v", got)
	}
}
