package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	minPasswordLength = 12
	maxPasswordLength = 128
	maxEmailLength    = 254
)

// Argon2id settings. Stored hashes record their own, so these can change later
// without breaking existing passwords.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 2
	argonSaltBytes = 16
	argonKeyBytes  = 32
)

// maxConcurrentHashes caps memory use. Each hash takes argonMemoryKiB, so a burst
// of logins must not be able to exhaust the machine.
const maxConcurrentHashes = 4

type hasher struct {
	slots chan struct{}
}

func newHasher() *hasher {
	return &hasher{slots: make(chan struct{}, maxConcurrentHashes)}
}

// acquire waits for a free slot and returns the function that releases it.
func (h *hasher) acquire(ctx context.Context) (func(), error) {
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// hash returns password in the standard encoded Argon2id format.
func (h *hasher) hash(ctx context.Context, password string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	salt := make([]byte, argonSaltBytes)
	_, _ = rand.Read(salt) // crypto/rand never returns an error.
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyBytes)
	encode := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, encode(salt), encode(key)), nil
}

// verify reports whether password matches encoded. An error means encoded is
// malformed or the request was cancelled, not that the password is wrong.
func (h *hasher) verify(ctx context.Context, encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("password hash has the wrong format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("password hash has an unsupported version")
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, errors.New("password hash has invalid parameters")
	}
	if iterations == 0 || threads == 0 {
		return false, errors.New("password hash has invalid parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("password hash has an invalid salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("password hash has an invalid key")
	}

	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	// Cast is safe: want came from a key length that fits in uint32.
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want))) //nolint:gosec
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func validatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < minPasswordLength || length > maxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

// normalizeEmail returns the form of an address used for storage and lookups.
// It reports false if raw isn't a plain address.
func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxEmailLength {
		return "", false
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", false
	}
	return email, true
}
