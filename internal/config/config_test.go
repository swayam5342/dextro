package config

import (
	"log/slog"
	"strings"
	"testing"
)

const validURL = "postgres://dextro:9f2c1ab7e4@localhost:5432/dextro"

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DEXTRO_DATABASE_URL": validURL}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestLoadReadsValues(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DEXTRO_DATABASE_URL": validURL,
		"DEXTRO_LISTEN_ADDR":  "127.0.0.1:9090",
		"DEXTRO_LOG_LEVEL":    "debug",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9090" || cfg.LogLevel != slog.LevelDebug || cfg.DatabaseURL != validURL {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing database url", map[string]string{}, "DEXTRO_DATABASE_URL is required"},
		{"wrong scheme", map[string]string{"DEXTRO_DATABASE_URL": "mysql://u:9f2c1ab7e4@h/d"}, "postgres:// scheme"},
		{"no host", map[string]string{"DEXTRO_DATABASE_URL": "postgres://u:9f2c1ab7e4@/d"}, "must include a host"},
		{"no password", map[string]string{"DEXTRO_DATABASE_URL": "postgres://u@h/d"}, "must include a password"},
		{"empty password", map[string]string{"DEXTRO_DATABASE_URL": "postgres://u:@h/d"}, "must include a password"},
		{"placeholder password", map[string]string{"DEXTRO_DATABASE_URL": "postgres://u:changeme@h/d"}, "placeholder"},
		{"placeholder in a longer value", map[string]string{"DEXTRO_DATABASE_URL": "postgres://u:please-CHANGEME-now@h/d"}, "placeholder"},
		{"common default password", map[string]string{"DEXTRO_DATABASE_URL": "postgres://u:postgres@h/d"}, "placeholder"},
		{"bad listen addr", map[string]string{"DEXTRO_DATABASE_URL": validURL, "DEXTRO_LISTEN_ADDR": "8080"}, "DEXTRO_LISTEN_ADDR"},
		{"bad log level", map[string]string{"DEXTRO_DATABASE_URL": validURL, "DEXTRO_LOG_LEVEL": "loud"}, "DEXTRO_LOG_LEVEL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.env))
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Load(env(map[string]string{"DEXTRO_LISTEN_ADDR": "nope", "DEXTRO_LOG_LEVEL": "loud"}))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	for _, want := range []string{"DEXTRO_LISTEN_ADDR", "DEXTRO_DATABASE_URL", "DEXTRO_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestLoadErrorsNeverContainThePassword(t *testing.T) {
	const password = "s3cretvalue"
	for _, raw := range []string{
		"postgres://u:" + password + "@h:notaport/d", // url.Parse fails and would quote this.
		"mysql://u:" + password + "@h/d",
	} {
		_, err := Load(env(map[string]string{"DEXTRO_DATABASE_URL": raw}))
		if err == nil {
			t.Fatalf("Load(%q) succeeded, want error", raw)
		}
		if strings.Contains(err.Error(), password) {
			t.Errorf("error leaks the password: %q", err)
		}
	}
}
