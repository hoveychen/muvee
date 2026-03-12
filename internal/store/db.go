package store

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
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
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
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
