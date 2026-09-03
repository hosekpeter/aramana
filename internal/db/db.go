package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aramana/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// retryInterval is the wait between connection attempts while the database starts up.
const retryInterval = 2 * time.Second

// Connect opens the pool and waits for the database to accept queries.
func Connect(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	poolConfig.MaxConns = cfg.DB.MaxConns
	poolConfig.MinConns = cfg.DB.MinConns
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute
	poolConfig.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	deadline := time.Now().Add(cfg.DB.ConnectTimeout)
	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := pool.Ping(ctx); err == nil {
			return pool, nil
		} else {
			lastErr = err
		}

		if ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("database connection cancelled after %d attempts: %w", attempt, errors.Join(ctx.Err(), lastErr))
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("database unreachable after %d attempts: %w", attempt, lastErr)
		}

		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("database connection cancelled: %w", errors.Join(ctx.Err(), lastErr))
		case <-time.After(retryInterval):
		}
	}
}

// RunMigrations applies the embedded migrations.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("database pool is nil")
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	goose.SetBaseFS(migrationFS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
