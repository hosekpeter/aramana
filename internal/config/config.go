// Package config loads configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

type Config struct {
	ServerPort int `env:"PORT" envDefault:"8080" validate:"gte=1,lte=65535"`
	DB         DBConfig
	Log        LogConfig
}

// DBConfig describes the database connection and the pool bounds.
type DBConfig struct {
	Host     string `env:"DB_HOST" envDefault:"localhost" validate:"required"`
	Port     int    `env:"DB_PORT" envDefault:"5432" validate:"gte=1,lte=65535"`
	User     string `env:"DB_USER" envDefault:"postgres" validate:"required"`
	Password string `env:"DB_PASSWORD" envDefault:"postgres"`
	Name     string `env:"DB_NAME" envDefault:"triage" validate:"required"`
	SSLMode  string `env:"DB_SSLMODE" envDefault:"disable"`

	MaxConns         int32         `env:"DB_MAX_CONNS" envDefault:"20" validate:"gtefield=MinConns"`
	MinConns         int32         `env:"DB_MIN_CONNS" envDefault:"2" validate:"gte=1"`
	StatementTimeout time.Duration `env:"DB_STATEMENT_TIMEOUT" envDefault:"5s" validate:"gt=0s"`
	ConnectTimeout   time.Duration `env:"DB_CONNECT_TIMEOUT" envDefault:"30s" validate:"gt=0s"`
}

// LogConfig controls the structured logger.
type LogConfig struct {
	Level   slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	Format  string     `env:"LOG_FORMAT" envDefault:"json"`
	Service string     `env:"LOG_SERVICE" envDefault:"adaptive-intelligent-triage"`
}

// Load reads the environment and returns a validated configuration.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// validate checks the configuration for missing or invalid values.
func (c Config) validate() error {
	err := validator.New().Struct(c)
	if err == nil {
		return nil
	}

	var invalid validator.ValidationErrors
	if !errors.As(err, &invalid) {
		return err
	}

	problems := make([]error, 0, len(invalid))

	for _, fe := range invalid {
		problems = append(problems, fmt.Errorf("%s=%v violates %s", fe.Field(), fe.Value(), rule(fe)))
	}

	return errors.Join(problems...)
}

func rule(fe validator.FieldError) string {
	if fe.Param() == "" {
		return fe.Tag()
	}
	return fe.Tag() + "=" + fe.Param()
}

// DSN returns the database connection string for pgx.
func (c Config) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&statement_timeout=%d",
		url.QueryEscape(c.DB.User),
		url.QueryEscape(c.DB.Password),
		c.DB.Host,
		c.DB.Port,
		c.DB.Name,
		c.DB.SSLMode,
		c.DB.StatementTimeout.Milliseconds(),
	)
}
