package apierr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"aramana/internal/service"
)

func TestFromError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want APIError
	}{
		{"session not found", service.ErrSessionNotFound, APIError{http.StatusNotFound, CodeSessionNotFound, "triage session not found"}},
		{"closed session", service.ErrSessionClosed, APIError{http.StatusConflict, CodeSessionClosed, "triage session is closed"}},
		{"wrong question", service.ErrQuestionMismatch, APIError{http.StatusConflict, CodeQuestionMismatch, "submitted answer does not match current question"}},
		{"unfinished session", service.ErrSessionNotComplete, APIError{http.StatusConflict, CodeSessionNotComplete, "triage session is not complete yet"}},
		{"reused idempotency key", service.ErrIdempotencyKeyReused, APIError{http.StatusUnprocessableEntity, CodeIdempotencyKeyReused, "idempotency key was already used with a different payload"}},
		{"unknown option", service.ErrOptionNotFound, APIError{http.StatusBadRequest, CodeOptionNotFound, "selected option does not belong to question"}},
		{"invalid request", service.ErrInvalidRequest, APIError{http.StatusBadRequest, CodeInvalidRequest, "invalid request"}},
		{"wrapped error", fmt.Errorf("context: %w", service.ErrSessionNotFound), APIError{http.StatusNotFound, CodeSessionNotFound, "triage session not found"}},
		{"unknown error", errors.New("database detail must not leak"), APIError{http.StatusInternalServerError, CodeInternalError, "internal server error"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, FromError(tt.err))
		})
	}
}
