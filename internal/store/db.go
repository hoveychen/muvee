package store

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Default budget for Connect to keep retrying before giving up. On a host
// reboot dockerd restores containers concurrently by restart policy and
// ignores Compose's depends_on, so the server can come up well before
// Postgres finishes recovering. Exiting on the first failed Ping turns that
// into a restart loop that only a manual `docker compose up -d` breaks.
const defaultConnectTimeout = 5 * time.Minute

// Bounds for the exponential backoff between Ping attempts.
const (
	connectRetryInitial = 1 * time.Second
	connectRetryMax     = 15 * time.Second
	// Each Ping gets its own deadline so an unreachable host that blackholes
	// SYNs can't stall the whole budget on a single attempt.
	connectPingTimeout = 5 * time.Second
)

func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://muvee:muvee@localhost:5432/muvee?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	budget := defaultConnectTimeout
	if v := os.Getenv("DB_CONNECT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("parse DB_CONNECT_TIMEOUT %q: %w", v, err)
		}
		budget = d
	}

	deadline := time.Now().Add(budget)
	backoff := connectRetryInitial
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, connectPingTimeout)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			if attempt > 1 {
				log.Printf("db: connected after %d attempts", attempt)
			}
			return pool, nil
		}
		if ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("ping db: %w", ctx.Err())
		}
		if !time.Now().Add(backoff).Before(deadline) {
			pool.Close()
			return nil, fmt.Errorf("ping db: giving up after %s (%d attempts): %w", budget, attempt, err)
		}
		log.Printf("db: not ready yet (attempt %d): %v; retrying in %s", attempt, err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("ping db: %w", ctx.Err())
		}
		if backoff *= 2; backoff > connectRetryMax {
			backoff = connectRetryMax
		}
	}
}

func Migrate(ctx context.Context, db *pgxpool.Pool, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		var exists bool
		_ = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename=$1)`, filename).Scan(&exists)
		if exists {
			continue
		}
		sql, err := os.ReadFile(fmt.Sprintf("%s/%s", migrationsDir, filename))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", filename, err)
		}
		if _, err := db.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", filename, err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, filename); err != nil {
			return fmt.Errorf("record migration %s: %w", filename, err)
		}
	}
	return nil
}
