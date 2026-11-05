// Package config reads and validates the environment at startup.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
)

// Config holds every setting the server reads from the environment.
type Config struct {
	ListenAddr  string
	DatabaseURL string
	LogLevel    slog.Level
}

const defaultListenAddr = ":8080"

// Load reads the configuration through getenv, normally os.Getenv. It reports
// every problem at once so a bad deployment can be fixed in one pass. Errors
// never contain secret values.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{ListenAddr: getenv("DEXTRO_LISTEN_ADDR"), DatabaseURL: getenv("DEXTRO_DATABASE_URL")}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = defaultListenAddr
	}

	var errs []error
	if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
		errs = append(errs, fmt.Errorf("DEXTRO_LISTEN_ADDR is not a valid host:port: %q", cfg.ListenAddr))
	}
	if err := validateDatabaseURL(cfg.DatabaseURL); err != nil {
		errs = append(errs, fmt.Errorf("DEXTRO_DATABASE_URL %w", err))
	}
	if level := getenv("DEXTRO_LOG_LEVEL"); level != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
			errs = append(errs, fmt.Errorf("DEXTRO_LOG_LEVEL is not a valid level: %q", level))
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateDatabaseURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	// url.Parse errors quote the input, which contains the password, so the
	// error is deliberately dropped.
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return errors.New("must use the postgres:// scheme")
	}
	if u.Hostname() == "" {
		return errors.New("must include a host")
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return errors.New("must include a password")
	}
	if isPlaceholder(password) {
		return errors.New("uses a placeholder password; set a real one")
	}
	return nil
}

// isPlaceholder reports whether a secret is a value copied from an example
// file rather than one somebody chose.
func isPlaceholder(secret string) bool {
	s := strings.ToLower(secret)
	for _, marker := range []string{"changeme", "change-me", "change_me", "replace", "placeholder", "example"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	switch s {
	case "password", "secret", "postgres", "admin", "test", "todo":
		return true
	}
	return false
}
