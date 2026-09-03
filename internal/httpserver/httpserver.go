// Package httpserver runs the HTTP listener with a bounded graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"adaptive-intelligent-triage/internal/api"
	"adaptive-intelligent-triage/internal/config"
	"adaptive-intelligent-triage/internal/dependencies"
)

// shutdownTimeout bounds how long in-flight requests get to finish.
const shutdownTimeout = 10 * time.Second

// Server wraps http.Server.
type Server struct {
	server *http.Server
	logger *slog.Logger
}

// New builds the server from the wired scope.
func New(scope dependencies.ServiceScope, cfg config.Config) *Server {
	return &Server{
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.ServerPort),
			Handler:           api.NewRouter(scope),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
		},
		logger: scope.Base().Logger(),
	}
}

func (s *Server) Start(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		s.logger.Info("HTTP server shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return <-serverErr
}
