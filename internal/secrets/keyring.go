// Package secrets encrypts credentials before they reach the database.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const keyBytes = 32

var (
	// ErrUnknownKey means a value was sealed with a key that is no longer listed.
	ErrUnknownKey = errors.New("encryption key is not configured")
	// ErrDecrypt means a value is corrupt, was tampered with, or belongs elsewhere.
	ErrDecrypt = errors.New("value could not be decrypted")
)

// Sealed is an encrypted value and the ID of the key that sealed it. Saving the
// ID is what allows keys to be rotated: new values use the active key while old
// ones stay readable.
type Sealed struct {
	KeyID      string
	Ciphertext []byte
}

// Keyring encrypts with one active key and decrypts with any listed key.
type Keyring struct {
	ciphers  map[string]cipher.AEAD
	activeID string
}

// NewKeyring returns a Keyring. Each key must be 32 bytes, for AES-256.
func NewKeyring(keys map[string][]byte, activeID string) (*Keyring, error) {
	ciphers := make(map[string]cipher.AEAD, len(keys))
	for id, key := range keys {
		if len(key) != keyBytes {
			return nil, fmt.Errorf("encryption key %q must be %d bytes", id, keyBytes)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("create cipher for key %q: %w", id, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("create GCM for key %q: %w", id, err)
		}
		ciphers[id] = aead
	}
	if _, ok := ciphers[activeID]; !ok {
		return nil, fmt.Errorf("active encryption key %q is not configured", activeID)
	}
	return &Keyring{ciphers: ciphers, activeID: activeID}, nil
}

// Encrypt seals plaintext. The context says what the value belongs to and must
// be given again to decrypt, so a value copied onto another row won't open.
func (k *Keyring) Encrypt(plaintext []byte, context string) Sealed {
	aead := k.ciphers[k.activeID]
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce) // crypto/rand never returns an error.
	return Sealed{
		KeyID:      k.activeID,
		Ciphertext: aead.Seal(nonce, nonce, plaintext, []byte(context)),
	}
}

// Decrypt opens a value sealed with the same context.
func (k *Keyring) Decrypt(sealed Sealed, context string) ([]byte, error) {
	aead, ok := k.ciphers[sealed.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, sealed.KeyID)
	}
	if len(sealed.Ciphertext) < aead.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ciphertext := sealed.Ciphertext[:aead.NonceSize()], sealed.Ciphertext[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(context))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
