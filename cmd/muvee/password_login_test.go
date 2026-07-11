package main

import (
	"bytes"
	"strings"
	"testing"
)

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
