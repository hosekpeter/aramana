package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"adaptive-intelligent-triage/internal/config"
	"adaptive-intelligent-triage/internal/dependencies"
	"adaptive-intelligent-triage/internal/httpserver"
	"adaptive-intelligent-triage/internal/logger"
)

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
