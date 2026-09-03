package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"aramana/internal/config"
	"aramana/internal/dependencies"
	"aramana/internal/httpserver"
	"aramana/internal/logger"
)

// @title       ARAMANA Adaptive Intelligent Triage API
// @version     1.0.0
// @description Adaptive mental-health triage. A client starts a session, submits one answer
// @description at a time, and receives either the next question or a final result. Because
// @description the next question depends on previous answers, a client must always answer
// @description the question the session reports as current rather than a fixed sequence.
// @description
// @description Two behaviours are worth reading before integrating:
// @description
// @description **Idempotency.** POST /answers accepts an optional `idempotencyKey` in the
// @description request **body** — it is not an HTTP header. Retrying with the same key
// @description replays the original response with `outcome: REPLAYED` instead of advancing
// @description the flow again, and stays safe even when the retry overlaps the original.
// @description
// @description **The safety gate.** A high-risk answer closes the session immediately with
// @description status `HIGH_RISK`; no further answers are accepted. Its result recommends
// @description `IMMEDIATE_SUPPORT`, an action that scoring alone never produces — which is
// @description what lets a consumer tell a crisis from a merely severe score.
// @description
// @description Scores, risk flags and question routing are deliberately absent from every
// @description response: they would reveal which option trips the safety gate.
// @tag.name        Triage
// @tag.description Triage sessions, answers and results
// @tag.name        Operations
// @tag.description Liveness and readiness probes
// @servers.url         http://localhost:8080
// @servers.description Local development
func main() {
	if err := run(); err != nil {
		slog.Error("service_stopped_with_error", logger.ErrorAttr(err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	scope, err := dependencies.NewServiceScope(ctx, cfg)
	if err != nil {
		return err
	}
	defer scope.Close()

	log := scope.Base().Logger()

	log.Info("triage service starting", "port", cfg.ServerPort)
	err = httpserver.New(scope, cfg).Start(ctx)
	log.Info("triage service stopped")
	return err
}
