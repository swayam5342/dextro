package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHashedPasswordsVerify(t *testing.T) {
	h := newHasher()
	ctx := context.Background()
	encoded, err := h.hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if strings.Contains(encoded, "correct horse") {
		t.Fatalf("hash contains the password: %s", encoded)
	}
	if !strings.HasPrefix(encoded, "$argon2id$") {
		t.Errorf("hash = %s, want argon2id", encoded)
	}

	ok, err := h.verify(ctx, encoded, "correct horse battery")
	if err != nil || !ok {
		t.Errorf("verify(right password) = %v, %v", ok, err)
	}
	ok, err = h.verify(ctx, encoded, "wrong password here")
	if err != nil || ok {
		t.Errorf("verify(wrong password) = %v, %v", ok, err)
	}
}

func TestHashingTheSamePasswordTwiceDiffers(t *testing.T) {
	h := newHasher()
	ctx := context.Background()
	first, _ := h.hash(ctx, "correct horse battery")
	second, _ := h.hash(ctx, "correct horse battery")
	if first == second {
		t.Error("two hashes are identical, so the salt isn't random")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h := newHasher()
	for _, encoded := range []string{
		"",
		"plaintext",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdA",
		"$argon2id$v=19$m=x,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
		"$bcrypt$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=0,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=0$c2FsdA$aGFzaA",
	} {
		if _, err := h.verify(context.Background(), encoded, "password"); err == nil {
			t.Errorf("verify(%q) succeeded, want error", encoded)
		}
	}
}

func TestHashingStopsWhenTheRequestIsCancelled(t *testing.T) {
	h := newHasher()
	for range cap(h.slots) {
		h.slots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.hash(ctx, "correct horse battery"); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		ok       bool
	}{
		{"too short", "elevenchars", false},
		{"just long enough", "twelve chars", true},
		{"counts characters, not bytes", "pässwörd pä", false},
		{"longest allowed", strings.Repeat("a", 128), true},
		{"too long", strings.Repeat("a", 129), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.password)
			if tt.ok && err != nil {
				t.Errorf("error = %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrWeakPassword) {
				t.Errorf("error = %v, want ErrWeakPassword", err)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		raw, want string
		ok        bool
	}{
		{"person@example.org", "person@example.org", true},
		{"  Person@Example.ORG ", "person@example.org", true},
		{"first.last+tag@example.org", "first.last+tag@example.org", true},
		{"", "", false},
		{"no-at-sign", "", false},
		{"Name <person@example.org>", "", false},
		{"person@", "", false},
		{"person@example.org\x00", "", false},
		{strings.Repeat("a", 250) + "@example.org", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeEmail(tt.raw)
		if ok != tt.ok || got != tt.want {
			t.Errorf("normalizeEmail(%q) = %q, %v, want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}
