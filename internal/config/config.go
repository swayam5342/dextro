// Package config reads and validates the environment at startup.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// Config holds every setting the server reads from the environment.
type Config struct {
	ListenAddr  string
	DatabaseURL string
	LogLevel    slog.Level
	// PublicURL is the origin browsers use, such as https://files.example.org.
	PublicURL string
	// TrustedProxies lists the peers whose X-Forwarded-For header is believed.
	TrustedProxies     []netip.Prefix
	GoogleClientID     string
	GoogleClientSecret string
	// EncryptionKeys maps key IDs to 32-byte keys for stored credentials. Old keys
	// stay listed so existing values can still be read.
	EncryptionKeys map[string][]byte
	// ActiveKeyID names the key new values are encrypted with.
	ActiveKeyID string
	// S3AllowedHosts are the only S3 endpoints allowed to be on a private address.
	// An entry is a host, or host:port to allow just that port.
	S3AllowedHosts []string
}

// GoogleEnabled reports whether Google sign-in is configured.
func (c Config) GoogleEnabled() bool {
	return c.GoogleClientID != ""
}

const defaultListenAddr = ":8080"

// Load reads the configuration through getenv, normally os.Getenv. It reports
// every problem at once so a bad deployment can be fixed in one pass. Errors
// never contain secret values.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:         getenv("DEXTRO_LISTEN_ADDR"),
		DatabaseURL:        getenv("DEXTRO_DATABASE_URL"),
		GoogleClientID:     getenv("DEXTRO_GOOGLE_CLIENT_ID"),
		GoogleClientSecret: getenv("DEXTRO_GOOGLE_CLIENT_SECRET"),
	}
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
	var err error
	if cfg.PublicURL, err = parsePublicURL(getenv("DEXTRO_PUBLIC_URL")); err != nil {
		errs = append(errs, fmt.Errorf("DEXTRO_PUBLIC_URL %w", err))
	}
	if cfg.TrustedProxies, err = parseTrustedProxies(getenv("DEXTRO_TRUSTED_PROXIES")); err != nil {
		errs = append(errs, fmt.Errorf("DEXTRO_TRUSTED_PROXIES %w", err))
	}
	if err := validateGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret); err != nil {
		errs = append(errs, err)
	}
	if cfg.EncryptionKeys, cfg.ActiveKeyID, err = parseEncryptionKeys(getenv("DEXTRO_ENCRYPTION_KEYS"), getenv("DEXTRO_ENCRYPTION_KEY_ID")); err != nil {
		errs = append(errs, err)
	}
	cfg.S3AllowedHosts = parseHosts(getenv("DEXTRO_S3_ALLOWED_HOSTS"))

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

// parsePublicURL reduces raw to the scheme and host browsers will send as their
// Origin: lowercase, with no trailing slash or default port.
func parsePublicURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("is not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("must use http or https")
	}
	if u.Hostname() == "" {
		return "", errors.New("must include a host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("must be only a scheme and host, with no path")
	}
	host := strings.ToLower(u.Host)
	if (scheme == "https" && u.Port() == "443") || (scheme == "http" && u.Port() == "80") {
		host = strings.TrimSuffix(host, ":"+u.Port())
	}
	return scheme + "://" + host, nil
}

// parseTrustedProxies reads a comma-separated list of addresses and CIDR ranges.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return nil, fmt.Errorf("has an entry that is not an address or CIDR range: %q", entry)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func validateGoogle(clientID, clientSecret string) error {
	if (clientID == "") != (clientSecret == "") {
		return errors.New("DEXTRO_GOOGLE_CLIENT_ID and DEXTRO_GOOGLE_CLIENT_SECRET must be set together")
	}
	if clientSecret != "" && isPlaceholder(clientSecret) {
		return errors.New("DEXTRO_GOOGLE_CLIENT_SECRET is a placeholder; set a real one")
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

const encryptionKeyBytes = 32

// parseEncryptionKeys reads a comma-separated list of id:base64key entries. Its
// errors never quote a value, because the values are secrets.
func parseEncryptionKeys(raw, activeID string) (map[string][]byte, string, error) {
	if raw == "" {
		return nil, "", errors.New("DEXTRO_ENCRYPTION_KEYS is required; run make env")
	}
	keys := map[string][]byte{}
	var lastID string
	for entry := range strings.SplitSeq(raw, ",") {
		id, encoded, found := strings.Cut(strings.TrimSpace(entry), ":")
		if !found || id == "" {
			return nil, "", errors.New("DEXTRO_ENCRYPTION_KEYS must be a list of id:base64key entries")
		}
		if isPlaceholder(encoded) {
			return nil, "", errors.New("DEXTRO_ENCRYPTION_KEYS has a placeholder key; set a real one")
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != encryptionKeyBytes {
			return nil, "", fmt.Errorf("DEXTRO_ENCRYPTION_KEYS key %q must be %d bytes of base64", id, encryptionKeyBytes)
		}
		if isBlank(key) {
			return nil, "", fmt.Errorf("DEXTRO_ENCRYPTION_KEYS key %q is all zeros", id)
		}
		if _, duplicate := keys[id]; duplicate {
			return nil, "", fmt.Errorf("DEXTRO_ENCRYPTION_KEYS lists key %q twice", id)
		}
		keys[id] = key
		lastID = id
	}
	if activeID == "" {
		if len(keys) > 1 {
			return nil, "", errors.New("DEXTRO_ENCRYPTION_KEY_ID is required when there are several keys")
		}
		return keys, lastID, nil
	}
	if _, ok := keys[activeID]; !ok {
		return nil, "", errors.New("DEXTRO_ENCRYPTION_KEY_ID names a key that isn't in DEXTRO_ENCRYPTION_KEYS")
	}
	return keys, activeID, nil
}

func isBlank(key []byte) bool {
	for _, b := range key {
		if b != 0 {
			return false
		}
	}
	return true
}

func parseHosts(raw string) []string {
	var hosts []string
	for entry := range strings.SplitSeq(raw, ",") {
		if host := strings.ToLower(strings.TrimSpace(entry)); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}
