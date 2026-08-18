package auth

import (
	"context"
	"testing"
)

// newTestService builds a Service with no store, so ReloadSettingsProviders
// falls through to the ENTRA_* / PLATFORM_ENTRA_LOGIN env fallback and the test
// needs no database.
func newTestService() *Service {
	return &Service{providers: map[string]Provider{}}
}

func TestReloadSettingsProviders_RegistersEntraFromEnv(t *testing.T) {
	t.Setenv("PLATFORM_ENTRA_LOGIN", "true")
	t.Setenv("ENTRA_TENANT_ID", testTenantGUID)
	t.Setenv("ENTRA_CLIENT_ID", "cid")
	t.Setenv("ENTRA_CLIENT_SECRET", "sec")
	t.Setenv("ENTRA_REDIRECT_URL", "")
	t.Setenv("BASE_DOMAIN", "muvee.example")

	svc := newTestService()
	if err := svc.ReloadSettingsProviders(context.Background()); err != nil {
		t.Fatalf("ReloadSettingsProviders: %v", err)
	}
	p, ok := svc.provider("entra")
	if !ok {
		t.Fatal("entra provider not registered")
	}
	if got, want := p.CanonicalRedirectURL(), "https://muvee.example/auth/entra/callback"; got != want {
		t.Errorf("redirect = %q, want %q", got, want)
	}
	if !p.OrgScoped() {
		t.Error("a GUID tenant should be org-scoped")
	}
	// The platform login page renders whatever ListProviders returns.
	var found bool
	for _, info := range svc.ListProviders() {
		if info.ID == "entra" && info.DisplayName == "Microsoft" {
			found = true
		}
	}
	if !found {
		t.Errorf("entra missing from ListProviders: %#v", svc.ListProviders())
	}
}

func TestReloadSettingsProviders_ToggleOffUnregisters(t *testing.T) {
	t.Setenv("PLATFORM_ENTRA_LOGIN", "true")
	t.Setenv("ENTRA_TENANT_ID", testTenantGUID)
	t.Setenv("ENTRA_CLIENT_ID", "cid")
	t.Setenv("ENTRA_CLIENT_SECRET", "sec")

	svc := newTestService()
	if err := svc.ReloadSettingsProviders(context.Background()); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if _, ok := svc.provider("entra"); !ok {
		t.Fatal("entra should be registered while the toggle is on")
	}

	t.Setenv("PLATFORM_ENTRA_LOGIN", "false")
	if err := svc.ReloadSettingsProviders(context.Background()); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if _, ok := svc.provider("entra"); ok {
		t.Error("entra should be unregistered after the toggle goes off")
	}
}

// A toggle flipped on before the credentials are filled in must not leave a
// dead button on the login page.
func TestReloadSettingsProviders_IncompleteCredsSkipRegistration(t *testing.T) {
	t.Setenv("PLATFORM_ENTRA_LOGIN", "true")
	t.Setenv("ENTRA_TENANT_ID", testTenantGUID)
	t.Setenv("ENTRA_CLIENT_ID", "cid")
	t.Setenv("ENTRA_CLIENT_SECRET", "")

	svc := newTestService()
	if err := svc.ReloadSettingsProviders(context.Background()); err != nil {
		t.Fatalf("ReloadSettingsProviders: %v", err)
	}
	if _, ok := svc.provider("entra"); ok {
		t.Error("entra should not be registered with an empty client secret")
	}
}

// A malformed tenant surfaces as an error (the admin save logs it) and must not
// tear down a provider that was working a moment ago.
func TestReloadSettingsProviders_BadTenantKeepsPreviousProvider(t *testing.T) {
	t.Setenv("PLATFORM_ENTRA_LOGIN", "true")
	t.Setenv("ENTRA_TENANT_ID", testTenantGUID)
	t.Setenv("ENTRA_CLIENT_ID", "cid")
	t.Setenv("ENTRA_CLIENT_SECRET", "sec")

	svc := newTestService()
	if err := svc.ReloadSettingsProviders(context.Background()); err != nil {
		t.Fatalf("first reload: %v", err)
	}

	t.Setenv("ENTRA_TENANT_ID", "../evil")
	if err := svc.ReloadSettingsProviders(context.Background()); err == nil {
		t.Fatal("expected an error for a malformed tenant")
	}
	if _, ok := svc.provider("entra"); !ok {
		t.Error("a failed reload must leave the previous provider registered")
	}
}

func TestPlatformEntraEnabled(t *testing.T) {
	t.Setenv("PLATFORM_ENTRA_LOGIN", "")
	if platformEntraEnabled(map[string]string{}) {
		t.Error("off by default")
	}
	if !platformEntraEnabled(map[string]string{"platform_entra_login_enabled": "true"}) {
		t.Error("setting true should enable")
	}
	// The setting wins over the env fallback in both directions.
	t.Setenv("PLATFORM_ENTRA_LOGIN", "true")
	if platformEntraEnabled(map[string]string{"platform_entra_login_enabled": "false"}) {
		t.Error("setting false must override the env fallback")
	}
	if !platformEntraEnabled(map[string]string{}) {
		t.Error("env fallback should apply when the setting is unwritten")
	}
}

func TestEntraConfigFromSettings(t *testing.T) {
	t.Setenv("ENTRA_TENANT_ID", "env-tenant")
	t.Setenv("ENTRA_CLIENT_ID", "env-cid")
	t.Setenv("ENTRA_CLIENT_SECRET", "env-sec")

	got := EntraConfigFromSettings(map[string]string{
		"entra_tenant_id": "db-tenant",
		"entra_client_id": " db-cid ",
	})
	if got.TenantID != "db-tenant" {
		t.Errorf("TenantID = %q, want settings value", got.TenantID)
	}
	if got.ClientID != "db-cid" {
		t.Errorf("ClientID = %q, want trimmed settings value", got.ClientID)
	}
	if got.ClientSecret != "env-sec" {
		t.Errorf("ClientSecret = %q, want env fallback", got.ClientSecret)
	}
}

func TestPlatformEntraRedirectURL(t *testing.T) {
	t.Setenv("ENTRA_REDIRECT_URL", "https://explicit.example/cb")
	t.Setenv("BASE_DOMAIN", "muvee.example")
	if got, want := platformEntraRedirectURL(), "https://explicit.example/cb"; got != want {
		t.Errorf("explicit override: got %q want %q", got, want)
	}

	t.Setenv("ENTRA_REDIRECT_URL", "")
	if got, want := platformEntraRedirectURL(), "https://muvee.example/auth/entra/callback"; got != want {
		t.Errorf("derived from BASE_DOMAIN: got %q want %q", got, want)
	}

	t.Setenv("BASE_DOMAIN", "")
	if got, want := platformEntraRedirectURL(), "http://localhost:8080/auth/entra/callback"; got != want {
		t.Errorf("local dev default: got %q want %q", got, want)
	}
}
