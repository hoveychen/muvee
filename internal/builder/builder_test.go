package builder

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRunCmdCapturesAllOutputOnFailure verifies that all stdout/stderr lines
// are delivered to logFn even when the command exits with a non-zero status.
//
// Before the fix, readLines goroutines ran without synchronisation — cmd.Wait()
// could return (and the caller could move on) before the goroutines finished
// draining the pipes, silently dropping tail-end log lines.
//
// To make the race reliably observable we add a small sleep in logFn so the
// goroutines are still processing lines when cmd.Wait() returns.
func TestRunCmdCapturesAllOutputOnFailure(t *testing.T) {
	const totalLines = 200

	// Shell script: print lines to stderr, then exit 1.
	// Use stderr because that's where docker buildx writes build output.
	script := fmt.Sprintf(
		`for i in $(seq 1 %d); do echo "log line $i" >&2; done; exit 1`,
		totalLines,
	)

	var mu sync.Mutex
	var captured []string
	logFn := func(line string) {
		// Small delay to widen the race window: without WaitGroup,
		// runCmd returns before these calls complete.
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
		t.Errorf("captured %d log lines, want %d (last few captured: %v)",
			got, totalLines, tail(captured, 5))
	}
}

// TestRunCmdCapturesStdoutAndStderr ensures lines from both streams are captured.
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
	got := len(captured)
	mu.Unlock()

	if got != 4 {
		t.Errorf("captured %d lines, want 4; got: %v", got, captured)
	}
}

func tail(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ── buildProxyPassthrough ─────────────────────────────────────────────────────

func TestBuildProxyPassthrough(t *testing.T) {
	cases := []struct {
		envVal string // value set for BUILDER_PROXY_PASSTHROUGH; "" means unset
		unset  bool   // when true, env var is not set at all
		want   bool
	}{
		// Default-on: unset or empty → inherit
		{unset: true, want: true},
		{envVal: "", want: true},
		// Explicit true-ish values
		{envVal: "true", want: true},
		{envVal: "TRUE", want: true},
		{envVal: "1", want: true},
		{envVal: "yes", want: true},
		{envVal: "YES", want: true},
		{envVal: "on", want: true},
		{envVal: "ON", want: true},
		{envVal: "anything-else", want: true},
		// Disable values (all case variants + leading/trailing space)
		{envVal: "false", want: false},
		{envVal: "FALSE", want: false},
		{envVal: "False", want: false},
		{envVal: "  false  ", want: false},
		{envVal: "0", want: false},
		{envVal: "no", want: false},
		{envVal: "NO", want: false},
		{envVal: "No", want: false},
		{envVal: "off", want: false},
		{envVal: "OFF", want: false},
		{envVal: "Off", want: false},
	}

	for _, tc := range cases {
		name := tc.envVal
		if tc.unset {
			name = "<unset>"
		}
		t.Run(name, func(t *testing.T) {
			if tc.unset {
				t.Setenv("BUILDER_PROXY_PASSTHROUGH", "")
				// t.Setenv sets to empty; we need truly unset for the unset case.
				// Use a sentinel: unset by re-setting to empty, which os.Getenv
				// returns as "" — same code path as unset in our switch.
			} else {
				t.Setenv("BUILDER_PROXY_PASSTHROUGH", tc.envVal)
			}
			got := buildProxyPassthrough()
			if got != tc.want {
				t.Errorf("BUILDER_PROXY_PASSTHROUGH=%q: got %v, want %v", tc.envVal, got, tc.want)
			}
		})
	}
}

// ── collectProxyBuildArgs ─────────────────────────────────────────────────────

func TestCollectProxyBuildArgs(t *testing.T) {
	// All proxy env var names the function inspects.
	allProxyVars := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy",
	}

	t.Run("passthrough disabled returns nil args and disabled log", func(t *testing.T) {
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "false")
		t.Setenv("HTTP_PROXY", "http://proxy:3128")
		t.Setenv("HTTPS_PROXY", "http://proxy:3128")

		got, msg := collectProxyBuildArgs()
		if got != nil {
			t.Errorf("expected nil args when passthrough=false, got %v", got)
		}
		if !strings.Contains(msg, "disabled") {
			t.Errorf("expected log message to mention 'disabled', got %q", msg)
		}
	})

	t.Run("no proxy vars set returns nil args and direct-access log", func(t *testing.T) {
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "true")
		for _, v := range allProxyVars {
			t.Setenv(v, "")
		}

		got, msg := collectProxyBuildArgs()
		if got != nil {
			t.Errorf("expected nil when all proxy vars are empty, got %v", got)
		}
		if !strings.Contains(msg, "no proxy vars") {
			t.Errorf("expected log message to mention 'no proxy vars', got %q", msg)
		}
	})

	t.Run("empty-string proxy vars are not forwarded", func(t *testing.T) {
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "true")
		// Explicitly set to empty (simulates .proxy.env with blank values)
		t.Setenv("HTTP_PROXY", "")
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("NO_PROXY", "")
		t.Setenv("http_proxy", "")
		t.Setenv("https_proxy", "")
		t.Setenv("no_proxy", "")

		got, _ := collectProxyBuildArgs()
		if len(got) != 0 {
			t.Errorf("expected no build-args for empty proxy vars, got %v", got)
		}
	})

	t.Run("only non-empty vars are forwarded", func(t *testing.T) {
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "true")
		for _, v := range allProxyVars {
			t.Setenv(v, "")
		}
		t.Setenv("HTTPS_PROXY", "http://proxy.internal:7390")
		t.Setenv("NO_PROXY", "localhost,127.0.0.1")

		got, msg := collectProxyBuildArgs()

		// Must contain exactly these two pairs, no others.
		want := []string{
			"--build-arg", "HTTPS_PROXY=http://proxy.internal:7390",
			"--build-arg", "NO_PROXY=localhost,127.0.0.1",
		}
		if len(got) != len(want) {
			t.Fatalf("len=%d, want %d; got=%v", len(got), len(want), got)
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("missing %q in result %v", w, got)
			}
		}
		// Log must list the forwarded key names.
		if !strings.Contains(msg, "HTTPS_PROXY") || !strings.Contains(msg, "NO_PROXY") {
			t.Errorf("log message missing forwarded key names: %q", msg)
		}
	})

	t.Run("all proxy vars forwarded when all set", func(t *testing.T) {
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "true")
		vals := map[string]string{
			"HTTP_PROXY":  "http://p:1",
			"HTTPS_PROXY": "http://p:2",
			"NO_PROXY":    "localhost",
			"ALL_PROXY":   "http://p:3",
			"FTP_PROXY":   "http://p:4",
			"http_proxy":  "http://p:5",
			"https_proxy": "http://p:6",
			"no_proxy":    "127.0.0.1",
			"all_proxy":   "http://p:7",
			"ftp_proxy":   "http://p:8",
		}
		for k, v := range vals {
			t.Setenv(k, v)
		}

		got, _ := collectProxyBuildArgs()

		// Expect 10 pairs = 20 elements.
		if len(got) != 20 {
			t.Fatalf("expected 20 elements (10 --build-arg pairs), got %d: %v", len(got), got)
		}
		// Every --build-arg must be followed by "KEY=VALUE".
		for i := 0; i < len(got); i += 2 {
			if got[i] != "--build-arg" {
				t.Errorf("element %d: expected \"--build-arg\", got %q", i, got[i])
			}
		}
		// Each expected pair must appear.
		for k, v := range vals {
			pair := k + "=" + v
			if !slices.Contains(got, pair) {
				t.Errorf("missing build-arg %q in %v", pair, got)
			}
		}
	})

	t.Run("result always alternates --build-arg / KEY=VALUE", func(t *testing.T) {
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "true")
		for _, v := range allProxyVars {
			t.Setenv(v, "")
		}
		t.Setenv("HTTP_PROXY", "http://x:1")
		t.Setenv("https_proxy", "http://x:2")

		got, _ := collectProxyBuildArgs()

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

	t.Run("passthrough default (BUILDER_PROXY_PASSTHROUGH unset)", func(t *testing.T) {
		// Simulate environment with no BUILDER_PROXY_PASSTHROUGH set (empty = unset).
		t.Setenv("BUILDER_PROXY_PASSTHROUGH", "")
		for _, v := range allProxyVars {
			t.Setenv(v, "")
		}
		t.Setenv("HTTP_PROXY", "http://default-proxy:3128")

		got, msg := collectProxyBuildArgs()

		if !slices.Contains(got, "HTTP_PROXY=http://default-proxy:3128") {
			t.Errorf("expected HTTP_PROXY to be forwarded by default; got %v", got)
		}
		if !strings.Contains(msg, "HTTP_PROXY") {
			t.Errorf("log message should mention forwarded key HTTP_PROXY, got %q", msg)
		}
	})
}
