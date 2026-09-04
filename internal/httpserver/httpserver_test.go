package httpserver

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"aramana/internal/config"
	"aramana/internal/dependencies"
)

type testBase struct{ logger *slog.Logger }

func (b testBase) Logger() *slog.Logger { return b.logger }

// testScope embeds the unused contracts: New only needs Base, while the real router will use
// the embedded contracts if a request is ever served.
type testScope struct {
	dependencies.TriageService
	dependencies.ReadinessChecker
	logger *slog.Logger
}

func (s testScope) Base() dependencies.Base { return testBase{logger: s.logger} }
func (s testScope) Readiness() dependencies.ReadinessChecker {
	return s.ReadinessChecker
}
func (s testScope) Triage() dependencies.TriageService { return s.TriageService }
func (testScope) Close()                               {}

func TestNewConfiguresHTTPServer(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	server := New(testScope{logger: logger}, config.Config{ServerPort: 9090})

	assert.Same(t, logger, server.logger)
	assert.Equal(t, ":9090", server.server.Addr)
	assert.NotNil(t, server.server.Handler)
	assert.Equal(t, 5*time.Second, server.server.ReadHeaderTimeout)
	assert.Equal(t, 10*time.Second, server.server.ReadTimeout)
	assert.Equal(t, 10*time.Second, server.server.WriteTimeout)
	assert.Equal(t, 30*time.Second, server.server.IdleTimeout)
}
