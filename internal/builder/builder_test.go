package builder

import (
	"context"
	"fmt"
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
