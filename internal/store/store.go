package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aramana/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflicting concurrent write")

// Querier is the subset of pgx used by the repository.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// UnitOfWork runs a function with a repository bound to a single database transaction,
// committing on nil error and rolling back otherwise.
type UnitOfWork interface {
	WithTx(ctx context.Context, fn func(repo TriageRepository) error) error
}

// TriageRepository is the typed data access contract. The service depends only on this, so
// it can be exercised with an in-memory fake and needs no database to test.
type TriageRepository interface {
	EntryQuestion(ctx context.Context) (model.Question, error)
	QuestionByID(ctx context.Context, questionID string) (model.Question, error)
	QuestionDomain(ctx context.Context, questionID string) (*string, error)
	OptionForQuestion(ctx context.Context, questionID, optionID string) (model.Option, error)

	CreateSession(ctx context.Context, session model.Session) error
	SessionByID(ctx context.Context, sessionID string) (model.Session, error)
	// LockSessionByID reads the session and holds a row lock until the surrounding
	// transaction ends. This serialises concurrent answers to the same session.
	LockSessionByID(ctx context.Context, sessionID string) (model.Session, error)
	UpdateSessionState(ctx context.Context, update model.SessionUpdate) error

	InsertAnswer(ctx context.Context, answer model.Answer) error
	AnswersForSession(ctx context.Context, sessionID string) ([]model.Answer, error)

	UpsertResult(ctx context.Context, sessionID string, result model.TriageResult) error
	ResultForSession(ctx context.Context, sessionID string) (model.TriageResult, error)

	AppendEvent(ctx context.Context, event model.Event) error

	ReserveIdempotencyKey(ctx context.Context, record IdempotencyRecord) (bool, error)
	CompleteIdempotencyKey(ctx context.Context, key string, response []byte) error
	FindIdempotentResponse(ctx context.Context, key string) (IdempotencyRecord, error)
}

// IdempotencyRecord is a stored response for a previously handled request. The fingerprint
// lets the service detect a key reused with a different payload.
type IdempotencyRecord struct {
	Key                string
	SessionID          string
	RequestFingerprint string

	ResponseBody []byte
}
