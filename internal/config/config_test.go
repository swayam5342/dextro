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
	cfg, err := Load(env(valid()))
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
	cfg, err := Load(env(valid("DEXTRO_LISTEN_ADDR", "127.0.0.1:9090", "DEXTRO_LOG_LEVEL", "debug")))
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

const validPublicURL = "https://files.example.org"

// valid returns the smallest environment that loads, plus any overrides.
func valid(overrides ...string) map[string]string {
	values := map[string]string{
		"DEXTRO_DATABASE_URL":    validURL,
		"DEXTRO_PUBLIC_URL":      validPublicURL,
		"DEXTRO_ENCRYPTION_KEYS": "k1:" + testKeyA,
	}
	for i := 0; i+1 < len(overrides); i += 2 {
		values[overrides[i]] = overrides[i+1]
	}
	return values
}

func TestLoadNormalizesThePublicURLToAnOrigin(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"https://files.example.org", "https://files.example.org"},
		{"https://files.example.org/", "https://files.example.org"},
		{"http://127.0.0.1:3000", "http://127.0.0.1:3000"},
		{"HTTPS://Files.Example.org", "https://files.example.org"},
		{"https://files.example.org:443", "https://files.example.org"},
		{"http://files.example.org:80", "http://files.example.org"},
		{"https://files.example.org:8443", "https://files.example.org:8443"},
		{"http://[::1]:80", "http://[::1]"},
	}
	for _, tt := range tests {
		cfg, err := Load(env(valid("DEXTRO_PUBLIC_URL", tt.raw)))
		if err != nil {
			t.Fatalf("Load(%q): %v", tt.raw, err)
		}
		if cfg.PublicURL != tt.want {
			t.Errorf("PublicURL for %q = %q, want %q", tt.raw, cfg.PublicURL, tt.want)
		}
	}
}

func TestLoadRejectsBadPublicURLs(t *testing.T) {
	for _, raw := range []string{"", "files.example.org", "ftp://files.example.org", "https://", "https://files.example.org/app", "https://u:p@files.example.org", "https://files.example.org?x=1"} {
		_, err := Load(env(valid("DEXTRO_PUBLIC_URL", raw)))
		if err == nil || !strings.Contains(err.Error(), "DEXTRO_PUBLIC_URL") {
			t.Errorf("Load(%q) error = %v, want one naming DEXTRO_PUBLIC_URL", raw, err)
		}
	}
}

func TestLoadParsesTrustedProxies(t *testing.T) {
	cfg, err := Load(env(valid("DEXTRO_TRUSTED_PROXIES", "172.30.0.10, 10.0.0.0/8,::1")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"172.30.0.10/32", "10.0.0.0/8", "::1/128"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.TrustedProxies, want)
	}
	for i, prefix := range cfg.TrustedProxies {
		if prefix.String() != want[i] {
			t.Errorf("TrustedProxies[%d] = %s, want %s", i, prefix, want[i])
		}
	}
}

func TestLoadTrustsNoProxyByDefault(t *testing.T) {
	cfg, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want none", cfg.TrustedProxies)
	}
}

func TestLoadRejectsBadTrustedProxies(t *testing.T) {
	_, err := Load(env(valid("DEXTRO_TRUSTED_PROXIES", "web")))
	if err == nil || !strings.Contains(err.Error(), "DEXTRO_TRUSTED_PROXIES") {
		t.Errorf("error = %v, want one naming DEXTRO_TRUSTED_PROXIES", err)
	}
}

func TestGoogleSignInIsOffWithoutCredentials(t *testing.T) {
	cfg, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GoogleEnabled() {
		t.Error("GoogleEnabled = true, want false")
	}
}

func TestGoogleSignInIsOnWithBothCredentials(t *testing.T) {
	cfg, err := Load(env(valid("DEXTRO_GOOGLE_CLIENT_ID", "id.apps.googleusercontent.com", "DEXTRO_GOOGLE_CLIENT_SECRET", "GOCSPX-9f2c1ab7e4")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.GoogleEnabled() {
		t.Error("GoogleEnabled = false, want true")
	}
}

func TestLoadRejectsPartialOrPlaceholderGoogleCredentials(t *testing.T) {
	tests := []struct {
		name          string
		id, secret    string
		wantInMessage string
	}{
		{"id only", "id.apps.googleusercontent.com", "", "must be set together"},
		{"secret only", "", "GOCSPX-9f2c1ab7e4", "must be set together"},
		{"placeholder secret", "id.apps.googleusercontent.com", "changeme", "placeholder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(valid("DEXTRO_GOOGLE_CLIENT_ID", tt.id, "DEXTRO_GOOGLE_CLIENT_SECRET", tt.secret)))
			if err == nil || !strings.Contains(err.Error(), tt.wantInMessage) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantInMessage)
			}
		})
	}
}

// base64 of 32 distinct bytes, so it isn't mistaken for a blank key.
const (
	testKeyA = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	testKeyB = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8="
)

func TestLoadReadsOneEncryptionKey(t *testing.T) {
	cfg, err := Load(env(valid("DEXTRO_ENCRYPTION_KEYS", "k1:"+testKeyA)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveKeyID != "k1" || len(cfg.EncryptionKeys["k1"]) != 32 {
		t.Errorf("active = %q, keys = %d", cfg.ActiveKeyID, len(cfg.EncryptionKeys))
	}
}

func TestLoadNeedsAnActiveKeyWhenThereAreSeveral(t *testing.T) {
	keys := "k1:" + testKeyA + ",k2:" + testKeyB
	if _, err := Load(env(valid("DEXTRO_ENCRYPTION_KEYS", keys))); err == nil || !strings.Contains(err.Error(), "DEXTRO_ENCRYPTION_KEY_ID") {
		t.Errorf("error = %v, want one naming DEXTRO_ENCRYPTION_KEY_ID", err)
	}
	cfg, err := Load(env(valid("DEXTRO_ENCRYPTION_KEYS", keys, "DEXTRO_ENCRYPTION_KEY_ID", "k2")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveKeyID != "k2" || len(cfg.EncryptionKeys) != 2 {
		t.Errorf("active = %q, keys = %d", cfg.ActiveKeyID, len(cfg.EncryptionKeys))
	}
}

func TestLoadRejectsBadEncryptionKeys(t *testing.T) {
	blank := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	tests := []struct {
		name, keys, activeID string
	}{
		{"missing", "", ""},
		{"placeholder", "changeme", ""},
		{"placeholder value", "k1:changeme", ""},
		{"no id", ":" + testKeyA, ""},
		{"not base64", "k1:!!!!", ""},
		{"too short", "k1:AAEC", ""},
		{"all zeros", "k1:" + blank, ""},
		{"duplicate id", "k1:" + testKeyA + ",k1:" + testKeyB, "k1"},
		{"unknown active key", "k1:" + testKeyA, "k2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := valid("DEXTRO_ENCRYPTION_KEYS", tt.keys, "DEXTRO_ENCRYPTION_KEY_ID", tt.activeID)
			_, err := Load(env(values))
			if err == nil || !strings.Contains(err.Error(), "DEXTRO_ENCRYPTION_KEY") {
				t.Fatalf("error = %v, want one naming the setting", err)
			}
		})
	}
}

func TestLoadErrorsNeverContainTheEncryptionKey(t *testing.T) {
	_, err := Load(env(valid("DEXTRO_ENCRYPTION_KEYS", "k1:"+testKeyA[:20])))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if strings.Contains(err.Error(), testKeyA[:20]) {
		t.Errorf("error leaks the key: %q", err)
	}
}

func TestLoadReadsS3AllowedHosts(t *testing.T) {
	cfg, err := Load(env(valid("DEXTRO_S3_ALLOWED_HOSTS", " seaweedfs , NAS.local:9000,")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.S3AllowedHosts) != 2 || cfg.S3AllowedHosts[0] != "seaweedfs" || cfg.S3AllowedHosts[1] != "nas.local:9000" {
		t.Errorf("S3AllowedHosts = %v", cfg.S3AllowedHosts)
	}
}

func TestLoadAllowsNoPrivateS3HostsByDefault(t *testing.T) {
	cfg, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.S3AllowedHosts) != 0 {
		t.Errorf("S3AllowedHosts = %v, want none", cfg.S3AllowedHosts)
	}
}
