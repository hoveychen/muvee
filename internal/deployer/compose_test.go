package deployer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildComposeOverrideYAMLNoWorkspace(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, 0, "", "")

	mustContain(t, got, "services:")
	mustContain(t, got, "  app:")
	mustContain(t, got, "    ports:")
	mustContain(t, got, `      - "0:8080"`)
	mustContain(t, got, `      muvee.domain_prefix: "foxy"`)
	mustContain(t, got, `      muvee.expose_port: "8080"`)

	if strings.Contains(got, "    volumes:") {
		t.Fatalf("override should not declare a volumes block when workspaceMount is empty:\n%s", got)
	}
}

func TestBuildComposeOverrideYAMLWithWorkspace(t *testing.T) {
	mount := "/srv/muvee/volumes/abc-123:/workspace:rw"
	got := buildComposeOverrideYAML("app", "foxy", 8080, 0, mount, "")

	mustContain(t, got, "    volumes:")
	mustContain(t, got, `      - "`+mount+`"`)

	// The volumes block must sit under the same service indentation as
	// ports/labels — guarding against accidental top-level emission.
	if i := strings.Index(got, "    volumes:"); i < 0 || strings.Index(got, "  app:") > i {
		t.Fatalf("volumes should appear after `  app:` and at service indent level:\n%s", got)
	}
}

func TestBuildComposeOverrideYAMLMemoryLimit(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, 0, "", "1600m")

	// Limit is injected on the expose service; memswap == mem disables swap
	// (mirrors the single-container deployer convention).
	mustContain(t, got, `    mem_limit: "1600m"`)
	mustContain(t, got, `    memswap_limit: "1600m"`)

	// It must sit under the expose service, not at top level.
	if i := strings.Index(got, "    mem_limit:"); i < 0 || strings.Index(got, "  app:") > i {
		t.Fatalf("mem_limit should appear after `  app:` at service indent level:\n%s", got)
	}
}

func TestBuildComposeOverrideYAMLNoMemoryLimit(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, 0, "", "")

	if strings.Contains(got, "mem_limit") {
		t.Fatalf("override must not declare mem_limit when memoryLimit is empty:\n%s", got)
	}
}

func TestBuildComposeOverrideYAMLFixedHostPort(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, 13000, "", "")

	mustContain(t, got, `      - "13000:8080"`)
	if strings.Contains(got, `"0:8080"`) {
		t.Fatalf("fixed-port override must not emit the dynamic 0:port mapping:\n%s", got)
	}
}

func TestComposeProjectName(t *testing.T) {
	if got := composeProjectName("foxy"); got != "muvee-foxy" {
		t.Fatalf("composeProjectName(\"foxy\") = %q, want %q", got, "muvee-foxy")
	}
}

func TestParseHostPort(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"0.0.0.0:32768", 32768, false},
		{"[::]:32768", 32768, false},
		{"0.0.0.0:32768\n[::]:32768", 32768, false},
		{"garbage", 0, true},
	}
	for _, c := range cases {
		got, err := parseHostPort(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseHostPort(%q) expected error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHostPort(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseHostPort(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestIsEmptyDir(t *testing.T) {
	tmp := t.TempDir()
	if !isEmptyDir(tmp) {
		t.Fatal("freshly created tempdir should be empty")
	}

	if err := os.WriteFile(filepath.Join(tmp, "f"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if isEmptyDir(tmp) {
		t.Fatal("tempdir with a file should not be reported as empty")
	}

	if isEmptyDir(filepath.Join(tmp, "does-not-exist")) {
		t.Fatal("missing path must NOT be reported as empty (would mistakenly trigger migration)")
	}
}

func TestRedactGitURL(t *testing.T) {
	cases := map[string]string{
		"https://user:token@github.com/owner/repo.git": "https://***@github.com/owner/repo.git",
		"https://github.com/owner/repo.git":            "https://github.com/owner/repo.git",
		"git@github.com:owner/repo.git":                "git@github.com:owner/repo.git",
	}
	for in, want := range cases {
		if got := redactGitURL(in); got != want {
			t.Errorf("redactGitURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPortIsListening(t *testing.T) {
	// Real `ss -ltn` output, including header + IPv4 + IPv6 LISTEN rows.
	ssOut := `State    Recv-Q   Send-Q   Local Address:Port   Peer Address:Port   Process
LISTEN   0        128      0.0.0.0:22           0.0.0.0:*
LISTEN   0        128      [::]:8080            [::]:*
LISTEN   0        128      127.0.0.1:53         0.0.0.0:*
`
	cases := []struct {
		port int
		want bool
	}{
		{22, true},
		{8080, true},
		{53, true},
		{80, false},
		{443, false},
	}
	for _, c := range cases {
		if got := portIsListening(ssOut, c.port); got != c.want {
			t.Errorf("portIsListening(%d) = %v, want %v", c.port, got, c.want)
		}
	}

	// Lines without LISTEN must be ignored — TIME_WAIT / ESTAB rows can have
	// the same port and would falsely look bound.
	estab := "ESTAB  0  0  10.0.0.1:9999  10.0.0.2:443"
	if portIsListening(estab, 9999) {
		t.Error("portIsListening must only flag LISTEN rows")
	}
}

func TestEnvWithoutProxy(t *testing.T) {
	// Must match proxyVarKeys in deployer.go and collectProxyBuildArgsFrom in builder.go.
	proxyVars := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy",
	}
	for _, k := range proxyVars {
		t.Setenv(k, "http://proxy.example.com:3128")
	}
	t.Setenv("SOME_OTHER_VAR", "keep-me")

	result := envWithoutProxy()

	for _, e := range result {
		key := strings.SplitN(e, "=", 2)[0]
		for _, banned := range proxyVars {
			if key == banned {
				t.Errorf("envWithoutProxy() must not contain %s, but got %q", banned, e)
			}
		}
	}

	found := false
	for _, e := range result {
		if e == "SOME_OTHER_VAR=keep-me" {
			found = true
			break
		}
	}
	if !found {
		t.Error("envWithoutProxy() dropped SOME_OTHER_VAR, which should have been preserved")
	}
}

func TestDeployerProxyPassthrough(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want bool
	}{
		{"true", true}, {"1", true}, {"yes", true}, {"on", true},
		{"TRUE", true}, {"YES", true},
		{"false", false}, {"0", false}, {"no", false}, {"off", false},
		{"", false}, {"anything-else", false},
	} {
		t.Setenv("DEPLOYER_PROXY_PASSTHROUGH", tc.val)
		if got := deployerProxyPassthrough(); got != tc.want {
			t.Errorf("deployerProxyPassthrough() with %q = %v, want %v", tc.val, got, tc.want)
		}
	}
}

func TestEnvForCompose_DefaultStripsProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	t.Setenv("ALL_PROXY", "socks5://proxy.example.com:1080")
	t.Setenv("DEPLOYER_PROXY_PASSTHROUGH", "")

	result := envForCompose()
	for _, e := range result {
		key := strings.SplitN(e, "=", 2)[0]
		if proxyVarKeys[key] {
			t.Errorf("envForCompose() default must strip proxy vars, but found %q", e)
		}
	}
}

func TestEnvForCompose_PassthroughAddsWhitelistOnly(t *testing.T) {
	// Passthrough re-adds proxy vars that envWithoutProxy stripped.
	// Non-proxy vars (e.g. PATH) are already present in the base; the
	// passthrough whitelist adds only the proxy vars, nothing else.
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
	t.Setenv("ALL_PROXY", "socks5://proxy.example.com:1080")
	t.Setenv("DEPLOYER_PROXY_PASSTHROUGH", "true")

	result := envForCompose()

	proxyCount := 0
	for _, e := range result {
		key := strings.SplitN(e, "=", 2)[0]
		if key == "HTTP_PROXY" || key == "ALL_PROXY" {
			proxyCount++
		}
	}
	if proxyCount != 2 {
		t.Errorf("envForCompose() with passthrough=true must include HTTP_PROXY and ALL_PROXY, got %d of 2", proxyCount)
	}
}

// TestRunCmdCompose_StripsProxyFromSubprocess verifies that the env wiring in
// runCmdCompose actually reaches the subprocess: a child process should not see
// HTTP_PROXY even when the parent process has it set.
func TestRunCmdCompose_StripsProxyFromSubprocess(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
	t.Setenv("DEPLOYER_PROXY_PASSTHROUGH", "")

	var captured string
	logFn := func(line string) { captured += line }

	// Use "env" to dump the subprocess environment and capture it via logFn.
	// "env" is a POSIX utility available on all Linux/macOS systems.
	if err := runCmdCompose(context.Background(), logFn, "env"); err != nil {
		t.Fatalf("runCmdCompose(env): %v", err)
	}

	for _, line := range strings.Split(captured, "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if proxyVarKeys[key] {
			t.Errorf("subprocess saw proxy var %q; runCmdCompose must strip it", line)
		}
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}
