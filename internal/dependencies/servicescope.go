package dependencies

import (
	"context"
	"fmt"
	"log/slog"

	"aramana/internal/config"
	"aramana/internal/db"
	"aramana/internal/logger"
	"aramana/internal/service"
	"aramana/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

type base struct {
	logger *slog.Logger
}

func (b *base) Logger() *slog.Logger { return b.logger }

type serviceScope struct {
	base   Base
	pool   *pgxpool.Pool
	triage TriageService
}

func (s *serviceScope) Base() Base                  { return s.base }
func (s *serviceScope) Readiness() ReadinessChecker { return s.pool }
func (s *serviceScope) Triage() TriageService       { return s.triage }

func (s *serviceScope) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// NewServiceScope connects to the database, applies migrations and wires the application.
func NewServiceScope(ctx context.Context, cfg config.Config) (ServiceScope, error) {
	l := logger.New(cfg.Log)

	pool, err := db.Connect(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}

	if err := db.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	txRunner := store.NewTxRunner(pool)

	return &serviceScope{
		base:   &base{logger: l.Logger()},
		pool:   pool,
		triage: service.New(txRunner, l.Logger()),
	}, nil
}
