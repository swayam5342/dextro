package secrets

import (
	"bytes"
	"errors"
	"testing"
)

func key(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, 32)
}

func newTestKeyring(t *testing.T) *Keyring {
	t.Helper()
	k, err := NewKeyring(map[string][]byte{"k1": key(1), "k2": key(2)}, "k2")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return k
}

func TestEncryptedValuesDecrypt(t *testing.T) {
	k := newTestKeyring(t)
	sealed := k.Encrypt([]byte("refresh-token"), "connection:1")
	if sealed.KeyID != "k2" {
		t.Errorf("KeyID = %q, want the active key", sealed.KeyID)
	}
	if bytes.Contains(sealed.Ciphertext, []byte("refresh-token")) {
		t.Error("ciphertext contains the plaintext")
	}
	got, err := k.Decrypt(sealed, "connection:1")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "refresh-token" {
		t.Errorf("Decrypt = %q", got)
	}
}

func TestEncryptingTwiceDiffers(t *testing.T) {
	k := newTestKeyring(t)
	first := k.Encrypt([]byte("same"), "c")
	second := k.Encrypt([]byte("same"), "c")
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Error("two encryptions are identical, so the nonce isn't random")
	}
}

func TestOldKeysStillDecryptAfterRotation(t *testing.T) {
	old, err := NewKeyring(map[string][]byte{"k1": key(1)}, "k1")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	sealed := old.Encrypt([]byte("value"), "c")
	got, err := newTestKeyring(t).Decrypt(sealed, "c")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "value" {
		t.Errorf("Decrypt = %q", got)
	}
}

func TestDecryptRefuses(t *testing.T) {
	k := newTestKeyring(t)
	sealed := k.Encrypt([]byte("value"), "connection:1")
	flipped := bytes.Clone(sealed.Ciphertext)
	flipped[len(flipped)-1] ^= 1

	tests := []struct {
		name    string
		sealed  Sealed
		context string
		want    error
	}{
		{"an unknown key", Sealed{KeyID: "gone", Ciphertext: sealed.Ciphertext}, "connection:1", ErrUnknownKey},
		{"a value moved from another row", sealed, "connection:2", ErrDecrypt},
		{"tampered data", Sealed{KeyID: sealed.KeyID, Ciphertext: flipped}, "connection:1", ErrDecrypt},
		{"another key's data", Sealed{KeyID: "k1", Ciphertext: sealed.Ciphertext}, "connection:1", ErrDecrypt},
		{"data shorter than a nonce", Sealed{KeyID: "k2", Ciphertext: []byte("x")}, "connection:1", ErrDecrypt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := k.Decrypt(tt.sealed, tt.context); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewKeyringValidates(t *testing.T) {
	if _, err := NewKeyring(map[string][]byte{"k1": key(1)}, "missing"); err == nil {
		t.Error("an unknown active key was accepted")
	}
	if _, err := NewKeyring(map[string][]byte{"k1": []byte("short")}, "k1"); err == nil {
		t.Error("a short key was accepted")
	}
}
