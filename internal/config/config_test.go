package config

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validConfig is deliberately explicit instead of relying on env defaults: validation tests
// should fail because of the one field under test, not because their fixture is incomplete.
func validConfig() Config {
	return Config{
		ServerPort: 8080,
		DB: DBConfig{
			Host:             "localhost",
			Port:             5432,
			User:             "postgres",
			Password:         "postgres",
			Name:             "triage",
			SSLMode:          "disable",
			MaxConns:         20,
			MinConns:         2,
			StatementTimeout: 5 * time.Second,
			ConnectTimeout:   30 * time.Second,
		},
		Log: LogConfig{Level: slog.LevelInfo, Format: "json", Service: "aramana"},
	}
}

func TestConfigDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "uses the configured connection settings and timeout",
			cfg:  validConfig(),
			want: "postgres://postgres:postgres@localhost:5432/triage?sslmode=disable&statement_timeout=5000",
		},
		{
			name: "escapes credentials that contain URL-reserved characters",
			cfg: func() Config {
				cfg := validConfig()
				cfg.DB.Host = "db.example.test"
				cfg.DB.Port = 5433
				cfg.DB.User = "user@example"
				cfg.DB.Password = "pa ss&word"
				cfg.DB.SSLMode = "require"
				cfg.DB.StatementTimeout = 1250 * time.Millisecond
				return cfg
			}(),
			want: "postgres://user%40example:pa+ss%26word@db.example.test:5433/triage?sslmode=require&statement_timeout=1250",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.cfg.DSN())
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*Config)
		wantErrors []string
	}{
		{
			name:   "accepts a complete valid configuration",
			mutate: func(*Config) {},
		},
		{
			name: "rejects a port outside the valid range",
			mutate: func(cfg *Config) {
				cfg.ServerPort = 65536
			},
			wantErrors: []string{"ServerPort=65536 violates lte=65535"},
		},
		{
			name: "rejects a missing database host",
			mutate: func(cfg *Config) {
				cfg.DB.Host = ""
			},
			wantErrors: []string{"Host= violates required"},
		},
		{
			name: "requires the maximum pool size to include the minimum",
			mutate: func(cfg *Config) {
				cfg.DB.MaxConns = 1
				cfg.DB.MinConns = 2
			},
			wantErrors: []string{"MaxConns=1 violates gtefield=MinConns"},
		},
		{
			name: "rejects non-positive database timeouts",
			mutate: func(cfg *Config) {
				cfg.DB.StatementTimeout = 0
				cfg.DB.ConnectTimeout = 0
			},
			wantErrors: []string{
				"StatementTimeout=0s violates gt=0s",
				"ConnectTimeout=0s violates gt=0s",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.validate()
			if len(tt.wantErrors) == 0 {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			for _, want := range tt.wantErrors {
				assert.ErrorContains(t, err, want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		want       Config
		wantErrors []string
	}{
		{
			name: "uses documented defaults when no environment is set",
			want: validConfig(),
		},
		{
			name: "parses explicit environment overrides",
			env: map[string]string{
				"PORT":                 "9090",
				"DB_HOST":              "postgres.internal",
				"DB_PORT":              "5433",
				"DB_USER":              "triage_app",
				"DB_PASSWORD":          "secret",
				"DB_NAME":              "triage_test",
				"DB_SSLMODE":           "require",
				"DB_MAX_CONNS":         "30",
				"DB_MIN_CONNS":         "3",
				"DB_STATEMENT_TIMEOUT": "1500ms",
				"DB_CONNECT_TIMEOUT":   "10s",
				"LOG_LEVEL":            "debug",
				"LOG_FORMAT":           "text",
				"LOG_SERVICE":          "triage-test",
			},
			want: Config{
				ServerPort: 9090,
				DB: DBConfig{
					Host:             "postgres.internal",
					Port:             5433,
					User:             "triage_app",
					Password:         "secret",
					Name:             "triage_test",
					SSLMode:          "require",
					MaxConns:         30,
					MinConns:         3,
					StatementTimeout: 1500 * time.Millisecond,
					ConnectTimeout:   10 * time.Second,
				},
				Log: LogConfig{Level: slog.LevelDebug, Format: "text", Service: "triage-test"},
			},
		},
		{
			name:       "rejects environment values that cannot be parsed",
			env:        map[string]string{"PORT": "not-a-port"},
			wantErrors: []string{"load config", "ServerPort"},
		},
		{
			name:       "rejects environment values that parse but violate a rule",
			env:        map[string]string{"DB_MIN_CONNS": "0"},
			wantErrors: []string{"load config", "MinConns=0 violates gte=1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnvironment(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			got, err := Load()
			if len(tt.wantErrors) == 0 {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
				return
			}

			require.Error(t, err)
			for _, want := range tt.wantErrors {
				assert.ErrorContains(t, err, want)
			}
		})
	}
}

func TestRule(t *testing.T) {
	t.Parallel()

	type sample struct {
		Required string `validate:"required"`
		Port     int    `validate:"gte=1"`
	}

	err := validator.New().Struct(sample{})
	var validationErrors validator.ValidationErrors
	require.ErrorAs(t, err, &validationErrors)

	rules := make(map[string]string, len(validationErrors))
	for _, fieldError := range validationErrors {
		rules[fieldError.Field()] = rule(fieldError)
	}

	assert.Equal(t, map[string]string{
		"Required": "required",
		"Port":     "gte=1",
	}, rules)
}

// clearConfigEnvironment makes Load tests independent of the process environment. t.Setenv
// cannot unset a variable, and an empty value has different semantics from an absent variable
// for some environment parsers, so preserve and restore the exact prior state ourselves.
func clearConfigEnvironment(t *testing.T) {
	t.Helper()

	keys := []string{
		"PORT",
		"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE",
		"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_STATEMENT_TIMEOUT", "DB_CONNECT_TIMEOUT",
		"LOG_LEVEL", "LOG_FORMAT", "LOG_SERVICE",
	}
	type originalValue struct {
		value string
		set   bool
	}
	original := make(map[string]originalValue, len(keys))
	for _, key := range keys {
		value, set := os.LookupEnv(key)
		original[key] = originalValue{value: value, set: set}
		require.NoError(t, os.Unsetenv(key))
	}

	t.Cleanup(func() {
		for _, key := range keys {
			value := original[key]
			if value.set {
				_ = os.Setenv(key, value.value)
				continue
			}
			_ = os.Unsetenv(key)
		}
	})
}
