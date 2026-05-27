package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── runCmd ─────────────────────────────────────────────────────────────────────

// TestRunCmdCapturesAllOutputOnFailure verifies that all stderr lines are
// delivered to logFn even when the command exits non-zero.
//
// The 50µs sleep inside logFn keeps the readLines goroutines alive while the
// child process has already exited, making the race reliably observable:
// without wg.Wait() before cmd.Wait() the goroutines could be dropped
// mid-drain, silently losing tail-end lines.
func TestRunCmdCapturesAllOutputOnFailure(t *testing.T) {
	const totalLines = 200

	script := fmt.Sprintf(
		`for i in $(seq 1 %d); do echo "log line $i" >&2; done; exit 1`,
		totalLines,
	)

	var mu sync.Mutex
	var captured []string
	logFn := func(line string) {
		time.Sleep(50 * time.Microsecond)
		mu.Lock()
		captured = append(captured, line)
		mu.Unlock()
	}

	err := runCmd(context.Background(), logFn, "sh", "-c", script)
	if err == nil {
		t.Fatal("expected non-nil error from failing command")
	}

	mu.Lock()
	got := len(captured)
	mu.Unlock()

	if got != totalLines {
		t.Errorf("captured %d log lines, want %d (last few: %v)",
			got, totalLines, tail(captured, 5))
	}
}

// TestRunCmdCapturesStdoutAndStderr ensures lines from both streams are
// captured with correct content (not just a matching line count).
func TestRunCmdCapturesStdoutAndStderr(t *testing.T) {
	script := `echo "out1"; echo "out2"; echo "err1" >&2; echo "err2" >&2; exit 1`

	var mu sync.Mutex
	var captured []string
	logFn := func(line string) {
		mu.Lock()
		captured = append(captured, line)
		mu.Unlock()
	}

	err := runCmd(context.Background(), logFn, "sh", "-c", script)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	mu.Lock()
	got := slices.Clone(captured)
	mu.Unlock()

	if len(got) != 4 {
		t.Errorf("captured %d lines, want 4; got: %v", len(got), got)
	}
	for _, want := range []string{"out1", "out2", "err1", "err2"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in captured lines %v", want, got)
		}
	}
}

func tail(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ── readLines ──────────────────────────────────────────────────────────────────

func TestReadLinesNoTrailingNewline(t *testing.T) {
	t.Parallel()
	var captured []string
	readLines(strings.NewReader("line1\nno_newline_at_end"), func(s string) {
		captured = append(captured, s)
	})
	if !slices.Equal(captured, []string{"line1", "no_newline_at_end"}) {
		t.Errorf("got %v, want [line1 no_newline_at_end]", captured)
	}
}

func TestReadLinesLongLine(t *testing.T) {
	t.Parallel()
	longLine := strings.Repeat("x", 9000) // exceeds the 4096-byte read buffer
	var captured []string
	readLines(strings.NewReader(longLine+"\n"), func(s string) {
		captured = append(captured, s)
	})
	if len(captured) != 1 {
		t.Fatalf("expected 1 line, got %d", len(captured))
	}
	if captured[0] != longLine {
		t.Errorf("long line mangled: got len=%d, want len=%d", len(captured[0]), len(longLine))
	}
}

func TestReadLinesEmptyLinesFiltered(t *testing.T) {
	t.Parallel()
	var captured []string
	readLines(strings.NewReader("a\n\n\nb"), func(s string) {
		captured = append(captured, s)
	})
	// Intentional: empty lines are dropped (docker buildx emits many blank separator lines).
	if !slices.Equal(captured, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b]", captured)
	}
}

// ── helpers for pure-function tests ───────────────────────────────────────────

// envFrom returns a getenv func backed by m.
// Keys absent from m return "" — identical to os.Getenv for unset vars,
// so passing an empty map faithfully represents "no env vars set".
func envFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

// ── buildProxyPassthroughFor ───────────────────────────────────────────────────

func TestBuildProxyPassthrough(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string // empty map = key absent (unset)
		want bool
	}{
		// Default-on: key absent (empty map) returns "" same as os.Getenv for unset
		{name: "<unset>", env: map[string]string{}, want: true},
		// Explicitly set to empty string — same code path as absent
		{name: `""`, env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": ""}, want: true},
		// Pure whitespace trims to "" → default on
		{name: `"  "`, env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "  "}, want: true},
		// Explicit true-ish values
		{name: "true", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "true"}, want: true},
		{name: "TRUE", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "TRUE"}, want: true},
		{name: "1", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "1"}, want: true},
		{name: "yes", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "yes"}, want: true},
		{name: "YES", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "YES"}, want: true},
		{name: "on", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "on"}, want: true},
		{name: "ON", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "ON"}, want: true},
		{name: "anything-else", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "anything-else"}, want: true},
		// "falsey" looks like a disable value but is not in the list → enabled
		{name: "falsey", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "falsey"}, want: true},
		// Disable values — all casing variants plus leading/trailing whitespace
		{name: "false", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "false"}, want: false},
		{name: "FALSE", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "FALSE"}, want: false},
		{name: "False", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "False"}, want: false},
		{name: `"  false  "`, env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "  false  "}, want: false},
		{name: `"\tfalse"`, env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "\tfalse"}, want: false},
		{name: "0", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "0"}, want: false},
		{name: "no", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "no"}, want: false},
		{name: "NO", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "NO"}, want: false},
		{name: "No", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "No"}, want: false},
		{name: "off", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "off"}, want: false},
		{name: "OFF", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "OFF"}, want: false},
		{name: "Off", env: map[string]string{"BUILDER_PROXY_PASSTHROUGH": "Off"}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildProxyPassthroughFor(envFrom(tc.env))
			if got != tc.want {
				t.Errorf("BUILDER_PROXY_PASSTHROUGH=%q: got %v, want %v",
					tc.env["BUILDER_PROXY_PASSTHROUGH"], got, tc.want)
			}
		})
	}
}

// ── collectProxyBuildArgsFrom ──────────────────────────────────────────────────

func TestCollectProxyBuildArgs(t *testing.T) {
	allProxyVars := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy",
	}

	// allEmpty builds a map with every proxy var set to "" so the host
	// environment cannot leak in. Extra entries override or add keys.
	allEmpty := func(extra ...map[string]string) map[string]string {
		m := make(map[string]string)
		for _, v := range allProxyVars {
			m[v] = ""
		}
		for _, e := range extra {
			for k, v := range e {
				m[k] = v
			}
		}
		return m
	}

	t.Run("passthrough disabled returns nil args, log shows actual value", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"BUILDER_PROXY_PASSTHROUGH": "false",
			"HTTP_PROXY":                "http://proxy:3128",
			"HTTPS_PROXY":               "http://proxy:3128",
		}
		got, msg := collectProxyBuildArgsFrom(envFrom(env))
		if got != nil {
			t.Errorf("expected nil args when passthrough=false, got %v", got)
		}
		if !strings.Contains(msg, "disabled") {
			t.Errorf("log must mention 'disabled', got %q", msg)
		}
		if !strings.Contains(msg, "BUILDER_PROXY_PASSTHROUGH=false") {
			t.Errorf("log must show actual configured value, got %q", msg)
		}
	})

	t.Run("passthrough disabled with no proxy vars logs disabled not empty", func(t *testing.T) {
		t.Parallel()
		// Disabled check must fire before the "no proxy vars" check.
		env := map[string]string{"BUILDER_PROXY_PASSTHROUGH": "0"}
		got, msg := collectProxyBuildArgsFrom(envFrom(env))
		if got != nil {
			t.Errorf("expected nil args, got %v", got)
		}
		if !strings.Contains(msg, "disabled") {
			t.Errorf("log must mention 'disabled', got %q", msg)
		}
		if strings.Contains(msg, "no proxy vars") {
			t.Errorf("disabled check must precede empty-vars check; log must not say 'no proxy vars': %q", msg)
		}
		if !strings.Contains(msg, "BUILDER_PROXY_PASSTHROUGH=0") {
			t.Errorf("log must show actual value '0', got %q", msg)
		}
	})

	t.Run("no proxy vars set returns nil args and direct-access log", func(t *testing.T) {
		t.Parallel()
		env := allEmpty(map[string]string{"BUILDER_PROXY_PASSTHROUGH": "true"})
		got, msg := collectProxyBuildArgsFrom(envFrom(env))
		if got != nil {
			t.Errorf("expected nil when all proxy vars are empty, got %v", got)
		}
		if !strings.Contains(msg, "no proxy vars") {
			t.Errorf("expected log to mention 'no proxy vars', got %q", msg)
		}
	})

	t.Run("empty-string proxy vars are not forwarded", func(t *testing.T) {
		t.Parallel()
		// All 10 proxy vars explicitly empty — none should be forwarded.
		env := allEmpty(map[string]string{"BUILDER_PROXY_PASSTHROUGH": "true"})
		got, _ := collectProxyBuildArgsFrom(envFrom(env))
		if len(got) != 0 {
			t.Errorf("expected no build-args for empty proxy vars, got %v", got)
		}
	})

	t.Run("only non-empty vars forwarded with exact ordered log", func(t *testing.T) {
		t.Parallel()
		env := allEmpty(map[string]string{
			"BUILDER_PROXY_PASSTHROUGH": "true",
			"HTTPS_PROXY":               "http://proxy.internal:7390",
			"NO_PROXY":                  "localhost,127.0.0.1",
		})
		got, msg := collectProxyBuildArgsFrom(envFrom(env))

		// Order must follow the definition order in the source (HTTPS before NO).
		want := []string{
			"--build-arg", "HTTPS_PROXY=http://proxy.internal:7390",
			"--build-arg", "NO_PROXY=localhost,127.0.0.1",
		}
		if !slices.Equal(got, want) {
			t.Errorf("args mismatch:\ngot:  %v\nwant: %v", got, want)
		}
		// Exact log match also prevents over-reporting of unset keys.
		wantMsg := "[proxy] forwarding into build: HTTPS_PROXY, NO_PROXY"
		if msg != wantMsg {
			t.Errorf("log = %q, want %q", msg, wantMsg)
		}
	})

	t.Run("all proxy vars forwarded in definition order", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"BUILDER_PROXY_PASSTHROUGH": "true",
			"HTTP_PROXY":                "http://p:1",
			"HTTPS_PROXY":               "http://p:2",
			"NO_PROXY":                  "localhost",
			"ALL_PROXY":                 "http://p:3",
			"FTP_PROXY":                 "http://p:4",
			"http_proxy":                "http://p:5",
			"https_proxy":               "http://p:6",
			"no_proxy":                  "127.0.0.1",
			"all_proxy":                 "http://p:7",
			"ftp_proxy":                 "http://p:8",
		}
		got, _ := collectProxyBuildArgsFrom(envFrom(env))

		// Exact ordered equality locks definition order as a contract.
		// If the implementation switches to map iteration, this test fails.
		want := []string{
			"--build-arg", "HTTP_PROXY=http://p:1",
			"--build-arg", "HTTPS_PROXY=http://p:2",
			"--build-arg", "NO_PROXY=localhost",
			"--build-arg", "ALL_PROXY=http://p:3",
			"--build-arg", "FTP_PROXY=http://p:4",
			"--build-arg", "http_proxy=http://p:5",
			"--build-arg", "https_proxy=http://p:6",
			"--build-arg", "no_proxy=127.0.0.1",
			"--build-arg", "all_proxy=http://p:7",
			"--build-arg", "ftp_proxy=http://p:8",
		}
		if !slices.Equal(got, want) {
			t.Errorf("args mismatch (order matters):\ngot:  %v\nwant: %v", got, want)
		}
	})

	t.Run("result always alternates --build-arg / KEY=VALUE", func(t *testing.T) {
		t.Parallel()
		env := allEmpty(map[string]string{
			"BUILDER_PROXY_PASSTHROUGH": "true",
			"HTTP_PROXY":                "http://x:1",
			"https_proxy":               "http://x:2",
		})
		got, _ := collectProxyBuildArgsFrom(envFrom(env))

		if len(got)%2 != 0 {
			t.Fatalf("odd number of elements %d: %v", len(got), got)
		}
		for i := 0; i < len(got); i += 2 {
			if got[i] != "--build-arg" {
				t.Errorf("position %d: want \"--build-arg\", got %q", i, got[i])
			}
			if got[i+1] == "" {
				t.Errorf("position %d: empty KEY=VALUE after --build-arg", i+1)
			}
		}
	})

	t.Run("passthrough default (key absent) forwards proxy vars", func(t *testing.T) {
		t.Parallel()
		// BUILDER_PROXY_PASSTHROUGH absent from map → default on.
		env := allEmpty(map[string]string{
			"HTTP_PROXY": "http://default-proxy:3128",
		})
		got, msg := collectProxyBuildArgsFrom(envFrom(env))

		if !slices.Contains(got, "HTTP_PROXY=http://default-proxy:3128") {
			t.Errorf("expected HTTP_PROXY forwarded by default; got %v", got)
		}
		if !strings.Contains(msg, "HTTP_PROXY") {
			t.Errorf("log should mention HTTP_PROXY, got %q", msg)
		}
	})
}

// ── Build() integration ───────────────────────────────────────────────────────

// TestBuild_UsesRepoRootAsContext locks in that the final positional argument
// passed to `docker buildx build` is the cloned-repo root (workDir), not the
// dockerfile's parent directory. Reproduces the bug where a project with
// DockerfilePath="subdir/My.Dockerfile" sent only `<workDir>/subdir` as build
// context, causing every `COPY <path>` outside that subdir to 404.
//
// Strategy: drop a single shim binary into a tempdir, symlink both `git` and
// `docker` to it, front-load PATH. The shim fakes the minimum:
//   - `git clone ... <dst>` → mkdir -p <dst>
//   - `git -C <dir> rev-parse HEAD` → emit a fake 14-char sha
//   - `docker ...` → dump argv (one per line) into a known file, exit 0
//
// Then we read the dumped argv, locate the `-f` flag value (the absolute
// dockerfile path the builder synthesised), and assert the last positional
// equals workDir — derived by stripping the relative DockerfilePath from the
// -f value. Using a 2-segment relative path makes the bug observable: the
// buggy code returns the 1-segment parent, the fix returns the 2-segment
// grandparent.
func TestBuild_UsesRepoRootAsContext(t *testing.T) {
	stubDir := t.TempDir()
	argsFile := filepath.Join(stubDir, "docker.args")

	shim := fmt.Sprintf(`#!/bin/sh
case "${0##*/}" in
  git)
    case "$1" in
      clone)
        last=
        for a in "$@"; do last="$a"; done
        mkdir -p "$last" || exit 1
        exit 0
        ;;
      -C)
        # $1=-C $2=<dir> $3=rev-parse $4=<ref>
        if [ "$3" = "rev-parse" ]; then
          echo "fakecommitsha1"
          exit 0
        fi
        ;;
    esac
    exit 1
    ;;
  docker)
    : > %q
    for a in "$@"; do printf '%%s\n' "$a" >> %q; done
    exit 0
    ;;
esac
exit 1
`, argsFile, argsFile)

	shimPath := filepath.Join(stubDir, "shim.sh")
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	for _, name := range []string{"git", "docker"} {
		if err := os.Symlink(shimPath, filepath.Join(stubDir, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	cfg := BuildConfig{
		ProjectID:      "test-bcrr",
		GitURL:         "https://example.invalid/repo.git",
		GitBranch:      "main",
		DockerfilePath: "subdir/My.Dockerfile",
		RegistryAddr:   "registry.test.local",
	}
	var logs []string
	_, err := Build(context.Background(), cfg, func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatalf("Build failed: %v\nlogs:\n%s", err, strings.Join(logs, "\n"))
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read %s: %v", argsFile, err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	var fPath string
	for i, a := range args {
		if a == "-f" && i+1 < len(args) {
			fPath = args[i+1]
			break
		}
	}
	if fPath == "" {
		t.Fatalf("no -f flag in docker args: %v", args)
	}

	buildCtx := args[len(args)-1]
	const rel = "/subdir/My.Dockerfile"
	if !strings.HasSuffix(fPath, rel) {
		t.Fatalf("-f path %q does not end with %q", fPath, rel)
	}
	wantCtx := strings.TrimSuffix(fPath, rel)

	if buildCtx != wantCtx {
		t.Errorf("build context = %q; want %q (= repo-root workDir, NOT dockerfile parent dir).\n"+
			"-f = %q\nfull docker args: %v",
			buildCtx, wantCtx, fPath, args)
	}
}
