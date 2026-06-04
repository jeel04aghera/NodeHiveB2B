// Package db opens the PostgreSQL connection pool used by every control-plane module.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the database/sql "pgx" driver for goose
	"github.com/pressly/goose/v3"

	"github.com/nodehive/gpu-platform/migrations"
)

// Open parses the DSN, builds a pool, and verifies connectivity with a ping.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}

// Migrate applies any pending goose migrations embedded in the binary. It is
// idempotent — goose records applied versions in the goose_db_version table and
// skips ones already run — so it is safe to call on every startup. This is how the
// schema gets created in container/Railway deployments, which only run the binary.
func Migrate(url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open migration db: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
