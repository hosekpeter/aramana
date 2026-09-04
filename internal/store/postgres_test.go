package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"unique violation", &pgconn.PgError{Code: uniqueViolation}, true},
		{"wrapped unique violation", fmt.Errorf("insert: %w", &pgconn.PgError{Code: uniqueViolation}), true},
		{"different PostgreSQL error", &pgconn.PgError{Code: "23503"}, false},
		{"ordinary error", errors.New("network error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isUniqueViolation(tt.err))
		})
	}
}
