package api

import (
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/auth"
)

// Enabling Entra via /admin/settings must surface it in the merged downstream
// provider list — that list feeds both the project Auth tab's SIGN-IN
// PROVIDERS checkboxes and knownProviderIDs (which validates a saved
// enabled_providers whitelist), so a missing entry would make the checkbox
// unsavable.
func TestMergeDownstreamProviders_IncludesEntra(t *testing.T) {
	got := mergeDownstreamProviders(
		[]auth.ProviderInfo{{ID: "feishu", DisplayName: "飞书 / Lark"}},
		auth.SocialConfigs{Entra: &auth.EntraConfig{TenantID: "common", ClientID: "cid", ClientSecret: "sec"}},
	)
	var display string
	for _, p := range got {
		if p.ID == "entra" {
			display = p.DisplayName
		}
	}
	if display == "" {
		t.Fatalf("expected entra in merged list, got %#v", got)
	}
	if display != "Microsoft" {
		t.Errorf("entra display name = %q, want %q", display, "Microsoft")
	}
}

// A disabled Entra config must not leak into the downstream list.
func TestMergeDownstreamProviders_OmitsDisabledEntra(t *testing.T) {
	got := mergeDownstreamProviders(nil, auth.SocialConfigs{})
	for _, p := range got {
		if p.ID == "entra" {
			t.Fatalf("entra should be absent when not enabled, got %#v", got)
		}
	}
}

// Settings-key plumbing: the credential keys must be accepted by the admin
// settings allowlist and must mark the provider set dirty so authservice
// reloads (otherwise a saved Entra app stays invisible on project subdomains
// until the container restarts). platform_entra_login_enabled is
// platform-only — it must NOT force an authservice reload.
func TestEntraSettingKeys(t *testing.T) {
	for _, k := range []string{"entra_enabled", "entra_tenant_id", "entra_client_id", "entra_client_secret"} {
		if !isSocialOAuthSettingKey(k) {
			t.Errorf("%s should trigger an authservice provider reload", k)
		}
	}
	if isSocialOAuthSettingKey("platform_entra_login_enabled") {
		t.Error("platform_entra_login_enabled is platform-side only; it must not trigger an authservice reload")
	}
	if !isPlatformProviderSettingKey("platform_entra_login_enabled") {
		t.Error("platform_entra_login_enabled should trigger a platform provider reload")
	}
	for _, k := range []string{"entra_tenant_id", "entra_client_id", "entra_client_secret", "entra_avatar_enabled"} {
		if !isPlatformProviderSettingKey(k) {
			t.Errorf("%s should trigger a platform provider reload", k)
		}
	}
	if isPlatformProviderSettingKey("discord_client_id") {
		t.Error("discord is downstream-only; it must not trigger a platform provider reload")
	}
}

// The allowlist in handleUpdateAdminSettings is a plain map literal, so guard
// it with a direct read: a typo there silently drops the admin's input.
func TestEntraKeysAreAllowedSettings(t *testing.T) {
	for _, k := range []string{
		"entra_enabled", "entra_tenant_id", "entra_client_id", "entra_client_secret",
		"entra_avatar_enabled", "platform_entra_login_enabled",
	} {
		if !allowedSettingKey(k) {
			t.Errorf("%s missing from the admin settings allowlist", k)
		}
	}
	if allowedSettingKey("entra_tenant_id_typo") {
		t.Error("allowedSettingKey should reject unknown keys")
	}
	// Both toggles are *_enabled-suffixed, which the handler validates as
	// strictly "true"/"false"; keep that contract visible.
	for _, k := range []string{"entra_enabled", "platform_entra_login_enabled"} {
		if !strings.HasSuffix(k, "_enabled") {
			t.Errorf("%s should be an _enabled toggle", k)
		}
	}
}
