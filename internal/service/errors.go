package service

import "errors"

// Domain errors of the triage service. The API layer maps these onto HTTP status codes and
// stable error codes; nothing below this line knows about HTTP.
var (
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionClosed means the session already reached a terminal state, either
	// COMPLETED or HIGH_RISK.
	ErrSessionClosed = errors.New("session is not active")
	// ErrQuestionMismatch means the answer does not target the question the session is
	// currently waiting on. Under concurrency this is what the losing writer sees.
	ErrQuestionMismatch = errors.New("question is not the expected current question")
	ErrOptionNotFound   = errors.New("option does not belong to the question")
	ErrInvalidRequest   = errors.New("invalid request")
	// ErrSessionNotComplete means a result was requested for a session that is still in
	// progress. This is a client-visible state, not an internal failure.
	ErrSessionNotComplete = errors.New("session is not complete")
	// ErrIdempotencyKeyReused means a known idempotency key arrived with a different
	// payload, which indicates a client bug rather than a retry.
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different payload")
)
