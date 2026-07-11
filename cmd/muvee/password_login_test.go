package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSignForwardPasswordJWT_CarriesEmail is the regression guard for the
// demo-account email gap: a demo login must bake the account's email into the
// forward JWT so setUserHeaders can populate X-Forwarded-User for downstream
// services. Before this fix the password JWT carried no email and downstream
// got an empty identity.
func TestSignForwardPasswordJWT_CarriesEmail(t *testing.T) {
	prev := jwtSecret
	jwtSecret = []byte("test-secret-for-password-jwt")
	defer func() { jwtSecret = prev }()

	signed, err := signForwardPasswordJWT("demo@example.com", "Demo User", "", "proj-123")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := parseForwardJWT(signed)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Email != "demo@example.com" {
		t.Errorf("email lost through the password JWT: got %q, want %q", claims.Email, "demo@example.com")
	}
	if claims.Provider != "password" || claims.ProjectID != "proj-123" {
		t.Errorf("provider/project binding changed: provider=%q project=%q", claims.Provider, claims.ProjectID)
	}

	// The claim must surface as X-Forwarded-User so the downstream app can read
	// the identity off the forward-auth response.
	rec := httptest.NewRecorder()
	setUserHeaders(rec, claims)
	if got := rec.Header().Get("X-Forwarded-User"); got != "demo@example.com" {
		t.Errorf("X-Forwarded-User = %q, want the demo account email", got)
	}
}

// TestLoginPageTmpl_PasswordForm verifies the username/password form renders
// exactly when PasswordLogin is set, and that the "or" divider only appears
// when OAuth provider buttons are also present.
func TestLoginPageTmpl_PasswordForm(t *testing.T) {
	render := func(data loginPageData) string {
		var buf bytes.Buffer
		if err := loginPageTmpl.Execute(&buf, data); err != nil {
			t.Fatalf("template execute: %v", err)
		}
		return buf.String()
	}

	off := render(loginPageData{
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	})
	if strings.Contains(off, `action="/_oauth/password"`) {
		t.Error("password form rendered although PasswordLogin=false")
	}

	formOnly := render(loginPageData{
		PasswordLogin: true,
		PrimaryColor:  safeColor("", "#4f46e5"),
		SidebarBg:     safeColor("", "#0f172a"),
	})
	if !strings.Contains(formOnly, `action="/_oauth/password"`) {
		t.Error("password form missing although PasswordLogin=true")
	}
	if !strings.Contains(formOnly, `name="username"`) || !strings.Contains(formOnly, `name="password"`) {
		t.Error("form inputs missing")
	}
	if strings.Contains(formOnly, `class="divider"`) {
		t.Error("divider rendered although there are no OAuth buttons to separate from")
	}

	withProviders := render(loginPageData{
		PasswordLogin: true,
		Providers:     []loginProviderItem{{Name: "google", DisplayName: "Google"}},
		PrimaryColor:  safeColor("", "#4f46e5"),
		SidebarBg:     safeColor("", "#0f172a"),
	})
	if !strings.Contains(withProviders, `class="divider"`) {
		t.Error("divider missing between OAuth buttons and password form")
	}
}

// TestLoginPageTmpl_PasswordErrorEscaped guards the error banner: it renders
// only when set and cannot smuggle HTML.
func TestLoginPageTmpl_PasswordErrorEscaped(t *testing.T) {
	data := loginPageData{
		PasswordLogin: true,
		LoginError:    `<script>alert(1)</script>`,
		PrimaryColor:  safeColor("", "#4f46e5"),
		SidebarBg:     safeColor("", "#0f172a"),
	}
	var buf bytes.Buffer
	if err := loginPageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `class="pw-error"`) {
		t.Error("error banner missing although LoginError set")
	}
	if strings.Contains(body, `<script>alert(1)</script>`) {
		t.Error("LoginError leaked unescaped HTML into output")
	}

	buf.Reset()
	data.LoginError = ""
	if err := loginPageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	if strings.Contains(buf.String(), `class="pw-error"`) {
		t.Error("error banner rendered although LoginError empty")
	}
}

// TestBuildLoginPageData_PasswordLoginFlag verifies the flag flows from the
// project auth config into the template payload (and stays off for nil cfg /
// apex hosts).
func TestBuildLoginPageData_PasswordLoginFlag(t *testing.T) {
	if got := buildLoginPageData(nil, nil); got.PasswordLogin {
		t.Error("nil cfg must not enable the password form")
	}
	if got := buildLoginPageData(&projectAuthConfig{PasswordLogin: false}, nil); got.PasswordLogin {
		t.Error("PasswordLogin=false must not enable the password form")
	}
	if got := buildLoginPageData(&projectAuthConfig{PasswordLogin: true}, nil); !got.PasswordLogin {
		t.Error("PasswordLogin=true lost on the way into the template payload")
	}
}
