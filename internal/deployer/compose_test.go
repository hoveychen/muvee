package deployer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildComposeOverrideYAMLNoWorkspace(t *testing.T) {
	got := buildComposeOverrideYAML("app", "foxy", 8080, "")

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
	got := buildComposeOverrideYAML("app", "foxy", 8080, mount)

	mustContain(t, got, "    volumes:")
	mustContain(t, got, `      - "`+mount+`"`)

	// The volumes block must sit under the same service indentation as
	// ports/labels — guarding against accidental top-level emission.
	if i := strings.Index(got, "    volumes:"); i < 0 || strings.Index(got, "  app:") > i {
		t.Fatalf("volumes should appear after `  app:` and at service indent level:\n%s", got)
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

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}
