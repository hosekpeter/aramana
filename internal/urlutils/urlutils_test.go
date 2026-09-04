package urlutils

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tls     bool
		forward string
		want    string
	}{
		{"plain HTTP", false, "", "http://example.test"},
		{"TLS", true, "", "https://example.test"},
		{"HTTPS proxy", false, "https", "https://example.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "http://example.test/path", nil)
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			req.Header.Set("X-Forwarded-Proto", tt.forward)
			assert.Equal(t, tt.want, GetBaseURL(req).String())
		})
	}
}
