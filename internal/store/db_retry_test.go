package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A port nothing listens on, so every Ping fails fast with "connection refused".
const unreachableDSN = "postgres://muvee:muvee@127.0.0.1:1/muvee?sslmode=disable&connect_timeout=1"

func TestConnectRetriesUntilBudgetExhausted(t *testing.T) {
	t.Setenv("DATABASE_URL", unreachableDSN)
	t.Setenv("DB_CONNECT_TIMEOUT", "3s")

	start := time.Now()
	pool, err := Connect(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		pool.Close()
		t.Fatal("Connect succeeded against an unreachable database; want error")
	}
	// Without retries this returns in milliseconds. The budget is 3s and the
	// backoff starts at 1s, so a retrying implementation spends at least one
	// sleep before giving up.
	if elapsed < connectRetryInitial {
		t.Errorf("Connect returned after %s; want at least %s (it did not retry)", elapsed, connectRetryInitial)
	}
	if elapsed > 10*time.Second {
		t.Errorf("Connect returned after %s; want it to respect the 3s budget", elapsed)
	}
	if !strings.Contains(err.Error(), "giving up") {
		t.Errorf("error = %v; want it to mention giving up after the budget", err)
	}
}

func TestConnectHonoursCancelledContext(t *testing.T) {
	t.Setenv("DATABASE_URL", unreachableDSN)
	t.Setenv("DB_CONNECT_TIMEOUT", "5m")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	start := time.Now()
	pool, err := Connect(ctx)
	if err == nil {
		pool.Close()
		t.Fatal("Connect succeeded; want error")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("Connect took %s after its context was cancelled; want it to bail out promptly", elapsed)
	}
}

func TestConnectRejectsBadTimeoutValue(t *testing.T) {
	t.Setenv("DATABASE_URL", unreachableDSN)
	t.Setenv("DB_CONNECT_TIMEOUT", "not-a-duration")

	pool, err := Connect(context.Background())
	if err == nil {
		pool.Close()
		t.Fatal("Connect accepted a malformed DB_CONNECT_TIMEOUT; want error")
	}
	if !strings.Contains(err.Error(), "DB_CONNECT_TIMEOUT") {
		t.Errorf("error = %v; want it to name DB_CONNECT_TIMEOUT", err)
	}
}
