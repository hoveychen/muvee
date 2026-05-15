package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestBuildLoginPageData_FallbackChain verifies the project -> platform ->
// built-in fallback order for every visible branding field, plus the
// hex-only safety wrapper on colour fields.
func TestBuildLoginPageData_FallbackChain(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *projectAuthConfig
		wantSite    string
		wantLogo    string
		wantPrimary string
		wantSidebar string
	}{
		{
			name:        "no project (apex host)",
			cfg:         nil,
			wantSite:    "", // template branches on empty to render the bare "Sign in" heading
			wantLogo:    "",
			wantPrimary: "#4f46e5",
			wantSidebar: "#0f172a",
		},
		{
			name: "project has nothing, platform has site+logo",
			cfg: &projectAuthConfig{
				ProjectName: "myapp",
				Branding: projectBranding{
					PlatformSiteName: "Muvee",
					PlatformLogoURL:  "https://cdn/platform.png",
				},
			},
			wantSite:    "Muvee",
			wantLogo:    "https://cdn/platform.png",
			wantPrimary: "#4f46e5",
			wantSidebar: "#0f172a",
		},
		{
			name: "project name only (no branding, no platform)",
			cfg: &projectAuthConfig{
				ProjectName: "myapp",
				Branding:    projectBranding{},
			},
			wantSite:    "myapp",
			wantLogo:    "",
			wantPrimary: "#4f46e5",
			wantSidebar: "#0f172a",
		},
		{
			name: "project branding wins over platform",
			cfg: &projectAuthConfig{
				Branding: projectBranding{
					SiteName:         "Acme",
					LogoURL:          "https://cdn/acme.png",
					PrimaryColor:     "#ff0066",
					PlatformSiteName: "Muvee",
					PlatformLogoURL:  "https://cdn/platform.png",
				},
			},
			wantSite:    "Acme",
			wantLogo:    "https://cdn/acme.png",
			wantPrimary: "#ff0066",
			wantSidebar: "#ff0066", // sidebar falls back to primary when unset
		},
		{
			name: "primary set, sidebar explicit overrides",
			cfg: &projectAuthConfig{
				Branding: projectBranding{
					PrimaryColor: "#ff0066",
					SidebarBg:    "#222233",
				},
			},
			wantSite:    "",
			wantLogo:    "",
			wantPrimary: "#ff0066",
			wantSidebar: "#222233",
		},
		{
			name: "junk colour values get dropped",
			cfg: &projectAuthConfig{
				Branding: projectBranding{
					PrimaryColor: "javascript:alert(1)",
					SidebarBg:    "red; }</style><script>x</script>",
				},
			},
			wantSite:    "",
			wantPrimary: "#4f46e5",
			wantSidebar: "#0f172a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := buildLoginPageData(tc.cfg, nil)
			if data.SiteName != tc.wantSite {
				t.Errorf("SiteName: got %q want %q", data.SiteName, tc.wantSite)
			}
			if data.LogoURL != tc.wantLogo {
				t.Errorf("LogoURL: got %q want %q", data.LogoURL, tc.wantLogo)
			}
			if string(data.PrimaryColor) != tc.wantPrimary {
				t.Errorf("PrimaryColor: got %q want %q", data.PrimaryColor, tc.wantPrimary)
			}
			if string(data.SidebarBg) != tc.wantSidebar {
				t.Errorf("SidebarBg: got %q want %q", data.SidebarBg, tc.wantSidebar)
			}
		})
	}
}

// TestLoginPageTmpl_EscapesUserContent renders the template with hostile
// branding strings and asserts that html/template escaped the dangerous
// payloads so they cannot break out of their respective contexts.
func TestLoginPageTmpl_EscapesUserContent(t *testing.T) {
	data := loginPageData{
		SiteName:     `Acme</title><script>alert("xss")</script>`,
		LogoURL:      `https://cdn/x.png" onerror="alert(1)`,
		Tagline:      `</div><img src=x onerror=alert(1)>`,
		Description:  `<b>bold</b>`,
		PrimaryColor: safeColor("nope", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	var buf bytes.Buffer
	if err := loginPageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	body := buf.String()
	// Raw <script> from the SiteName must NOT have rendered as an actual tag.
	if strings.Contains(body, `<script>alert("xss")</script>`) {
		t.Error("script payload from SiteName leaked unescaped into output")
	}
	// The onerror attribute injection on the logo URL must have been escaped
	// so the resulting <img> tag still has a single src attribute.
	if strings.Contains(body, `onerror="alert(1)`) {
		t.Error("onerror payload from LogoURL leaked unescaped into output")
	}
	// Tagline's <img> tag must have been escaped, not rendered.
	if strings.Contains(body, `<img src=x onerror=alert(1)>`) {
		t.Error("img injection in Tagline leaked unescaped into output")
	}
	// Confirm the safe defaults are still inlined in the style block.
	if !strings.Contains(body, "background:#4f46e5") && !strings.Contains(body, "border-color:#4f46e5") {
		t.Error("expected fallback primary colour #4f46e5 in inlined style")
	}
	if !strings.Contains(body, "background:#0f172a") {
		t.Error("expected fallback sidebar colour #0f172a in inlined style")
	}
}

// TestLoginPageTmpl_FaviconAndFooter verifies the conditional <link
// rel="icon"> and footer render only when the corresponding branding
// field is non-empty, and disappear when empty. The footer test also
// guards against the old hard-coded "Project access" text leaking back in
// (Boss flagged it as meaningless to downstream end-users).
func TestLoginPageTmpl_FaviconAndFooter(t *testing.T) {
	withBoth := loginPageData{
		SiteName:     "Acme",
		FaviconURL:   "https://cdn/favicon.ico",
		FooterText:   "© 2026 Acme — support@acme.com",
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	var buf bytes.Buffer
	if err := loginPageTmpl.Execute(&buf, withBoth); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `<link rel="icon" href="https://cdn/favicon.ico">`) {
		t.Error("expected favicon link element when FaviconURL set")
	}
	if !strings.Contains(body, "© 2026 Acme — support@acme.com") {
		t.Error("expected footer text when FooterText set")
	}
	if strings.Contains(body, "Single sign-on") || strings.Contains(body, "Project access") {
		t.Error("legacy hard-coded footer text leaked into output")
	}

	// Now the empty case: both fields blank, neither element should render.
	empty := loginPageData{
		SiteName:     "Acme",
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	buf.Reset()
	if err := loginPageTmpl.Execute(&buf, empty); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body = buf.String()
	if strings.Contains(body, `<link rel="icon"`) {
		t.Error("favicon link rendered despite empty FaviconURL")
	}
	if strings.Contains(body, `class="footer"`) {
		t.Error("footer div rendered despite empty FooterText")
	}
}

// TestLoginPageTmpl_CardCopyUsesSiteName guards the 2C-product copy
// rework: the card heading now interpolates SiteName ("Sign in to Acme"
// rather than the generic "Sign in to continue"), the trust row replaced
// the old "Access is restricted to invited members." footer-style line,
// and none of the previous hard-coded phrases must reappear.
func TestLoginPageTmpl_CardCopyUsesSiteName(t *testing.T) {
	data := loginPageData{
		SiteName:     "Acme",
		TrustItems:   []string{"Encrypted", "SSO", "OAuth verified"},
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	var buf bytes.Buffer
	if err := loginPageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()

	if !strings.Contains(body, "Sign in to Acme") {
		t.Error("expected heading to interpolate SiteName")
	}
	if !strings.Contains(body, `class="trust"`) {
		t.Error("expected trust-indicator row in rendered card")
	}
	for _, banned := range []string{
		"Sign in to continue",
		"Authorized users only",
		"Access is restricted to invited members",
		"invited members",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("legacy copy %q leaked back into login template", banned)
		}
	}

	// Empty SiteName must NOT degenerate into "Sign in to Sign in" — the
	// template branches on emptiness and falls back to a bare "Sign in"
	// heading. This is the apex-host path where no project was matched.
	empty := loginPageData{
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	buf.Reset()
	if err := loginPageTmpl.Execute(&buf, empty); err != nil {
		t.Fatalf("execute empty: %v", err)
	}
	emptyBody := buf.String()
	if strings.Contains(emptyBody, "Sign in to Sign in") {
		t.Error("empty SiteName produced nonsensical 'Sign in to Sign in' heading")
	}
	if !strings.Contains(emptyBody, ">Sign in<") {
		t.Error("expected bare 'Sign in' fallback heading when SiteName is empty")
	}
}

// TestParseTrustItems verifies the comma-split parser used to feed the
// per-project trust-row override. Empty / whitespace-only / pure-comma
// input all collapse to the built-in defaults; non-empty input is split
// on commas, trimmed, and capped at 3 entries so an over-eager owner
// can't blow out the card layout.
func TestParseTrustItems(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		// Empty / whitespace / comma-only inputs all return nil so the
		// template skips the trust row entirely — "leave blank to hide".
		{"empty hides row", "", nil},
		{"whitespace hides row", "   ", nil},
		{"only commas hide row", ",,,", nil},
		{"single entry", "GDPR ready", []string{"GDPR ready"}},
		{"two entries with whitespace", "SOC 2 ,  HIPAA", []string{"SOC 2", "HIPAA"}},
		{"three entries", "Encrypted,SOC 2,GDPR", []string{"Encrypted", "SOC 2", "GDPR"}},
		{"more than 3 capped", "a,b,c,d,e", []string{"a", "b", "c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTrustItems(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d: got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestLoginPageTmpl_EmptyTrustHidesRow guards the "leave blank to hide"
// contract: when TrustItems is nil the trust <div> must not render. This
// is the path project owners use to opt out of the trust badges entirely.
func TestLoginPageTmpl_EmptyTrustHidesRow(t *testing.T) {
	data := loginPageData{
		SiteName:     "Acme",
		TrustItems:   nil,
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	var buf bytes.Buffer
	if err := loginPageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, `class="trust"`) {
		t.Error("trust div rendered despite nil TrustItems")
	}
}

// TestLoginPageTmpl_TrustRowRenders verifies the template loops over
// TrustItems (so custom branding flows in) and escapes the entries so a
// malicious comma-separated value cannot inject markup into the trust
// row's <span> contents.
func TestLoginPageTmpl_TrustRowRenders(t *testing.T) {
	data := loginPageData{
		SiteName:     "Acme",
		TrustItems:   []string{"SOC 2", "HIPAA", `<script>alert(1)</script>`},
		PrimaryColor: safeColor("", "#4f46e5"),
		SidebarBg:    safeColor("", "#0f172a"),
	}
	var buf bytes.Buffer
	if err := loginPageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{"SOC 2", "HIPAA"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected trust item %q in output", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("script payload from TrustItems leaked unescaped into output")
	}
}
