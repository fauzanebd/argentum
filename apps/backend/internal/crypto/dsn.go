// Package crypto provides AES-GCM symmetric encryption for DSNs and tenant
// credentials at rest. The 32-byte key is loaded from the ARGENTUM_DSN_KEY env
// var (hex-encoded) at startup; a missing key in production is a fatal config
// error.
//
// The cipher itself, its wire format and the rotation keyring are in
// keyring.go (T-H14). This file holds what is shared underneath them.
package crypto

import (
	"crypto/rand"
	"fmt"
	"io"
)

// newNonce returns a fresh random nonce of the AEAD's size.
//
// One place, because a nonce reused across two payloads under the same key
// breaks GCM completely — not "weakens", breaks: it leaks the XOR of the
// plaintexts and forges the authenticator. There is no code path here that
// derives a nonce from anything but the CSPRNG.
func newNonce(size int) ([]byte, error) {
	nonce := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return nonce, nil
}
