package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSignForwardProjectJWT_Phone verifies a phone session bakes the phone into
// the forward JWT (surfaced as X-Forwarded-User) and stays project-scoped with
// provider "phone".
func TestSignForwardProjectJWT_Phone(t *testing.T) {
	prev := jwtSecret
	jwtSecret = []byte("test-secret-for-phone-jwt")
	defer func() { jwtSecret = prev }()

	signed, err := signForwardProjectJWT("+8613800138000", "+8613800138000", "", "phone", "proj-9")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := parseForwardJWT(signed)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Provider != "phone" || claims.ProjectID != "proj-9" {
		t.Errorf("provider/project binding wrong: provider=%q project=%q", claims.Provider, claims.ProjectID)
	}
	rec := httptest.NewRecorder()
	setUserHeaders(rec, claims)
	if got := rec.Header().Get("X-Forwarded-User"); got != "+8613800138000" {
		t.Errorf("X-Forwarded-User = %q, want the phone number", got)
	}
}

// TestHandleVerify_PhoneProjectBinding verifies a phone session is admitted only
// on its own project (project_id match) and bounced to login otherwise.
func TestHandleVerify_PhoneProjectBinding(t *testing.T) {
	prev := jwtSecret
	jwtSecret = []byte("test-secret-for-phone-verify")
	defer func() { jwtSecret = prev }()

	signed, err := signForwardProjectJWT("+8613800138000", "+8613800138000", "", "phone", "proj-A")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	cases := []struct {
		name       string
		projectID  string
		wantStatus int
	}{
		{"same project admitted", "proj-A", http.StatusOK},
		{"other project bounced", "proj-B", http.StatusFound},
		{"missing project bounced", "", http.StatusFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := "/verify"
			if c.projectID != "" {
				url += "?project_id=" + c.projectID
			}
			r := httptest.NewRequest(http.MethodGet, url, nil)
			r.AddCookie(&http.Cookie{Name: "muvee_fwd_session", Value: signed})
			w := httptest.NewRecorder()
			handleVerify(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, c.wantStatus)
			}
			if c.wantStatus == http.StatusOK && w.Header().Get("X-Forwarded-User") != "+8613800138000" {
				t.Errorf("admitted request missing X-Forwarded-User")
			}
		})
	}
}

// TestLoginPageTmpl_SMSForm verifies the phone form renders exactly when
// SMSLogin is set, with the divider only when something precedes it.
func TestLoginPageTmpl_SMSForm(t *testing.T) {
	render := func(data loginPageData) string {
		var buf bytes.Buffer
		if err := loginPageTmpl.Execute(&buf, data); err != nil {
			t.Fatalf("template execute: %v", err)
		}
		return buf.String()
	}
	base := func(d loginPageData) loginPageData {
		d.PrimaryColor = safeColor("", "#4f46e5")
		d.SidebarBg = safeColor("", "#0f172a")
		return d
	}

	off := render(base(loginPageData{}))
	if strings.Contains(off, `action="/_oauth/sms/verify"`) {
		t.Error("sms form rendered although SMSLogin=false")
	}

	smsOnly := render(base(loginPageData{SMSLogin: true}))
	if !strings.Contains(smsOnly, `action="/_oauth/sms/verify"`) {
		t.Error("sms form missing although SMSLogin=true")
	}
	if !strings.Contains(smsOnly, `/_oauth/sms/send`) {
		t.Error("send-code fetch target missing from sms form script")
	}
	if strings.Contains(smsOnly, `class="divider"`) {
		t.Error("divider rendered although nothing precedes the sms form")
	}

	withPw := render(base(loginPageData{SMSLogin: true, PasswordLogin: true}))
	if !strings.Contains(withPw, `class="divider"`) {
		t.Error("divider missing between password and sms forms")
	}
}

// TestLoginPageTmpl_SMSErrorEscaped guards the SMS error banner against HTML
// injection.
func TestLoginPageTmpl_SMSErrorEscaped(t *testing.T) {
	var buf bytes.Buffer
	err := loginPageTmpl.Execute(&buf, loginPageData{
		SMSLogin:     true,
		SMSError:     `<script>alert(1)</script>`,
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	})
	if err != nil {
		t.Fatalf("template execute: %v", err)
	}
	if strings.Contains(buf.String(), `<script>alert(1)</script>`) {
		t.Error("SMSError leaked unescaped HTML")
	}
}

// TestBuildLoginPageData_SMSLoginFlag verifies the flag flows from the auth
// config into the template payload.
func TestBuildLoginPageData_SMSLoginFlag(t *testing.T) {
	if got := buildLoginPageData(nil, nil); got.SMSLogin {
		t.Error("nil cfg must not enable the sms form")
	}
	if got := buildLoginPageData(&projectAuthConfig{SMSLogin: false}, nil); got.SMSLogin {
		t.Error("SMSLogin=false must not enable the sms form")
	}
	if got := buildLoginPageData(&projectAuthConfig{SMSLogin: true}, nil); !got.SMSLogin {
		t.Error("SMSLogin=true lost on the way into the template payload")
	}
}
