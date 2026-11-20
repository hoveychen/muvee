package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckRef(t *testing.T) {
	for _, bad := range []string{"--output=/tmp/x", "-p", "main\n--all"} {
		if checkRef(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for _, ok := range []string{"HEAD", "main", "feature/x", "a1b2c3", "v1.2.3"} {
		if err := checkRef(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

// ListCommits with an option-shaped ref must not create the --output file.
func TestListCommitsOutputInjection(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "r")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "x"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	target := filepath.Join(dir, "pwned")
	if _, err := ListCommits(repo, "--output="+target, 5); err == nil {
		t.Fatal("expected error for option-shaped ref")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("git log --output injection wrote a file")
	}
	if c, err := ListCommits(repo, "", 5); err != nil || len(c) != 1 {
		t.Fatalf("normal listing broken: %v %v", c, err)
	}
}
