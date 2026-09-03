package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"aramana/internal/apierr"
	"aramana/internal/ids"
)

type requestContextKey string

const requestIDContextKey requestContextKey = "request_id"

const maxRequestBodyBytes = 16 << 10

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func requestContext(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = ids.NewString()
			}

			ctx := context.WithValue(r.Context(), requestIDContextKey, requestID)
			r = r.WithContext(ctx)
			w.Header().Set("X-Request-ID", requestID)
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)

			logger.Info("request_finished",
				"request_id", requestID,
				"duration", time.Since(startedAt).Nanoseconds(),
				slog.Group("http",
					"method", r.Method,
					"url", r.URL.Path,
					"status_code", wrapped.Status(),
					"response_size", wrapped.BytesWritten(),
				),
			)
		})
	}
}

func recoverPanic(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				logger.Error("request_panic",
					"request_id", requestIDFromContext(r.Context()),
					slog.Group("http", "method", r.Method, "url", r.URL.Path),
					slog.Group("error",
						"kind", fmt.Sprintf("%T", recovered),
						"message", fmt.Sprint(recovered),
						"stack", string(debug.Stack()),
					),
				)
				writeJSON(w, http.StatusInternalServerError, apierr.Response{
					Code:      apierr.CodeInternalError,
					Message:   "internal server error",
					RequestID: requestIDFromContext(r.Context()),
				})
			}()
			next.ServeHTTP(w, r)
		})
	}
}
