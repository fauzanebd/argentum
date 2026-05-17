package lark

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// VerifySignature validates the X-Lark-Signature header. Lark documents the
// signature as `base64(sha256(timestamp + nonce + encrypt_key + body))` when
// the app has encrypt_key configured. When encrypt_key is empty the platform
// does not send a signature header — callers should skip verification.
func VerifySignature(encryptKey, timestamp, nonce, signature string, body []byte) error {
	if encryptKey == "" {
		return errors.New("encrypt_key required for signature verification")
	}
	if signature == "" {
		return errors.New("missing signature")
	}
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write([]byte(nonce))
	h.Write([]byte(encryptKey))
	h.Write(body)
	want := h.Sum(nil)

	// Lark's signature is sometimes hex, sometimes base64 — accept either.
	if got, err := hex.DecodeString(signature); err == nil && hmac.Equal(got, want) {
		return nil
	}
	if got, err := base64.StdEncoding.DecodeString(signature); err == nil && hmac.Equal(got, want) {
		return nil
	}
	return errors.New("signature mismatch")
}

// Decrypt opens an AES-256-CBC encrypted Lark event payload. The encrypt_key
// from the dev console is hashed once with SHA-256 to derive the AES key.
// The ciphertext is base64-encoded; the first 16 bytes after decoding are
// the IV.
func Decrypt(encryptKey, encryptedB64 string) ([]byte, error) {
	if encryptKey == "" {
		return nil, errors.New("encrypt_key required for decrypt")
	}
	ct, err := base64.StdEncoding.DecodeString(encryptedB64)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted payload: %w", err)
	}
	if len(ct) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}
	keySum := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keySum[:])
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	iv, body := ct[:aes.BlockSize], ct[aes.BlockSize:]
	if len(body)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext not aligned to block size")
	}
	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, body)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func pkcs7Unpad(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 || len(b)%blockSize != 0 {
		return nil, errors.New("invalid padding length")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > blockSize {
		return nil, errors.New("invalid padding byte")
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid padding")
		}
	}
	return b[:len(b)-pad], nil
}
