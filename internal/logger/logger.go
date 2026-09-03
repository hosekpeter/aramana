package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"aramana/internal/config"
)

// Logger is the default implementation.
type Logger struct {
	logger *slog.Logger
}

// New builds a Logger with the configured level and format.
func New(cfg config.LogConfig) *Logger {
	options := &slog.HandlerOptions{
		Level:       cfg.Level,
		ReplaceAttr: renameReservedKeys,
	}

	var handler slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		handler = slog.NewTextHandler(os.Stdout, options)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, options)
	}

	// Attached once here rather than at every call site, so no log line can be missing them.
	logger := slog.New(handler).With(
		"service", cfg.Service,
	)

	return &Logger{logger: logger}
}

func renameReservedKeys(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return attr
	}
	switch attr.Key {
	case slog.LevelKey:
		attr.Key = "status"
	case slog.MessageKey:
		attr.Key = "message"
	case slog.TimeKey:
		attr.Key = "timestamp"
	}
	return attr
}

// Logger returns the configured logger.
func (a *Logger) Logger() *slog.Logger {
	return a.logger
}

func ErrorAttr(err error) slog.Attr {
	return slog.Group("error",
		"kind", fmt.Sprintf("%T", err),
		"message", err.Error(),
	)
}
