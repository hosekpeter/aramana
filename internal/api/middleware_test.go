package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCORSMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		wantNext bool
	}{
		{"preflight stops at middleware", http.MethodOptions, false},
		{"normal request reaches handler", http.MethodGet, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var nextCalled atomic.Bool
			handler := corsMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled.Store(true)
				w.WriteHeader(http.StatusNoContent)
			}))

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tt.method, "/", nil))

			assert.Equal(t, "*", recorder.Header().Get("Access-Control-Allow-Origin"))
			assert.Contains(t, recorder.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
			assert.Equal(t, tt.wantNext, nextCalled.Load())
		})
	}
}
