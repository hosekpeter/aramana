package dependencies

import (
	"context"
	"log/slog"

	"adaptive-intelligent-triage/internal/model"
	"adaptive-intelligent-triage/internal/service"
)

// Base holds process-wide concerns.
type Base interface {
	Logger() *slog.Logger
}

// ServiceScope is the wired application.
type ServiceScope interface {
	Base() Base
	Readiness() ReadinessChecker
	Triage() TriageService
	Close()
}

// TriageService is the use-case contract consumed by the HTTP layer.
type TriageService interface {
	CreateSession(ctx context.Context) (*model.SessionState, error)
	GetSession(ctx context.Context, sessionID string) (*model.SessionState, error)
	SubmitAnswer(ctx context.Context, cmd service.SubmitAnswerCommand) (*service.SubmitAnswerResult, error)
	GetResult(ctx context.Context, sessionID string) (*model.TriageResult, error)
}

// ReadinessChecker reports whether the service's dependencies are usable.
type ReadinessChecker interface {
	Ping(ctx context.Context) error
}
