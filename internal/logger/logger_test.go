package logger

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenameReservedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		groups []string
		key    string
		want   string
	}{
		{"root level", nil, slog.LevelKey, "status"},
		{"root message", nil, slog.MessageKey, "message"},
		{"root time", nil, slog.TimeKey, "timestamp"},
		{"custom root key", nil, "session_status", "session_status"},
		{"nested level", []string{"http"}, slog.LevelKey, slog.LevelKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renameReservedKeys(tt.groups, slog.String(tt.key, "value"))
			assert.Equal(t, tt.want, got.Key)
		})
	}
}
