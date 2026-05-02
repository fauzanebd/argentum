// Package crypto provides AES-GCM symmetric encryption for DSNs at rest. The
// 32-byte key is loaded from the ARGENTUM_DSN_KEY env var (hex-encoded) at
// startup; missing key in production is a fatal config error.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// DSNCipher seals and opens DSN payloads using AES-256-GCM.
//
// Wire format on disk:  [12-byte nonce][ciphertext + 16-byte tag]
type DSNCipher struct {
	aead cipher.AEAD
}

// NewFromHex parses a 64-char hex-encoded 32-byte key.
func NewFromHex(hexKey string) (*DSNCipher, error) {
	if hexKey == "" {
		return nil, errors.New("ARGENTUM_DSN_KEY is required")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode hex key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("expected 32-byte key, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &DSNCipher{aead: aead}, nil
}

// Encrypt seals plaintext into nonce||ciphertext form.
func (c *DSNCipher) Encrypt(plain string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ct := c.aead.Seal(nil, nonce, []byte(plain), nil)
	return append(nonce, ct...), nil
}

// Decrypt opens a previously encrypted payload back to plaintext.
func (c *DSNCipher) Decrypt(blob []byte) (string, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns+c.aead.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	plain, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}
