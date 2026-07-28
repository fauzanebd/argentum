package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

// testKey is a fixed 32-byte key. Fixed rather than random so a failure is
// reproducible from the test output alone.
const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func newCipher(t *testing.T, hexKey string) *DSNCipher {
	t.Helper()
	c, err := NewFromHex(hexKey)
	if err != nil {
		t.Fatalf("NewFromHex(%q): %v", hexKey, err)
	}
	return c
}

func TestNewFromHexRejectsBadKeys(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr string
	}{
		{"empty", "", "required"},
		{"not hex", strings.Repeat("z", 64), "decode hex key"},
		{"odd length", strings.Repeat("a", 63), "decode hex key"},
		// A 16-byte key is valid AES — it just isn't AES-256, and silently
		// accepting it would halve the strength of every stored DSN.
		{"16 bytes", strings.Repeat("ab", 16), "expected 32-byte key"},
		{"31 bytes", strings.Repeat("ab", 31), "expected 32-byte key"},
		{"33 bytes", strings.Repeat("ab", 33), "expected 32-byte key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewFromHex(tc.key)
			if err == nil {
				t.Fatalf("NewFromHex(%q) succeeded, want error", tc.key)
			}
			if c != nil {
				t.Errorf("cipher = %v, want nil on error", c)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewFromHexAcceptsUppercase(t *testing.T) {
	// Operators paste keys from password managers, which are not consistent
	// about case. encoding/hex accepts both; this pins that it stays that way.
	lower := newCipher(t, testKey)
	upper := newCipher(t, strings.ToUpper(testKey))

	blob, err := upper.Encrypt("postgres://u:p@h/db")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := lower.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt with the same key in lowercase: %v", err)
	}
	if got != "postgres://u:p@h/db" {
		t.Errorf("round-trip = %q", got)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newCipher(t, testKey)

	cases := []struct {
		name  string
		plain string
	}{
		{"empty", ""},
		{"postgres dsn", "postgres://analytics:s3cr3t@10.0.0.4:5432/warehouse?sslmode=require"},
		{"sqlserver dsn", "sqlserver://sa:P@ss w0rd!@192.168.1.10:1433?database=Sales&TrustServerCertificate=true"},
		{"unicode", "postgres://pengguna:sandi@localhost/gudang_données?sslmode=disable"},
		{"long", strings.Repeat("postgres://u:p@h:5432/d?opt=1&", 200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blob, err := c.Encrypt(tc.plain)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			// The plaintext must not survive into the stored blob — a DSN
			// carries the tenant's warehouse password.
			if tc.plain != "" && bytes.Contains(blob, []byte(tc.plain)) {
				t.Error("ciphertext contains the plaintext verbatim")
			}
			got, err := c.Decrypt(blob)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if got != tc.plain {
				t.Errorf("round-trip = %q, want %q", got, tc.plain)
			}
		})
	}
}

func TestEncryptUsesAFreshNonce(t *testing.T) {
	// GCM nonce reuse under one key is a catastrophic failure, not a cosmetic
	// one: two DSNs sealed with the same nonce leak their XOR. Two seals of
	// the same plaintext must differ.
	c := newCipher(t, testKey)
	const plain = "postgres://u:p@h:5432/d"

	first, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of one plaintext are byte-identical: the nonce is not fresh")
	}
	if bytes.Equal(first[:12], second[:12]) {
		t.Error("nonce repeated across two encryptions")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	c := newCipher(t, testKey)
	other := newCipher(t, strings.Repeat("ff", 32))

	blob, err := c.Encrypt("postgres://u:p@h:5432/d")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := other.Decrypt(blob)
	if err == nil {
		t.Fatalf("Decrypt with the wrong key returned %q, want an error", got)
	}
	if got != "" {
		t.Errorf("plaintext = %q on failure, want empty", got)
	}
}

func TestDecryptMalformedInputErrorsRatherThanPanics(t *testing.T) {
	c := newCipher(t, testKey)

	valid, err := c.Encrypt("postgres://u:p@h:5432/d")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tampered := append([]byte(nil), valid...)
	tampered[len(tampered)-1] ^= 0xff

	flippedNonce := append([]byte(nil), valid...)
	flippedNonce[0] ^= 0xff

	cases := []struct {
		name string
		blob []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		// Anything shorter than nonce+tag cannot be a payload at all. This is
		// the branch that would slice out of range without the length check.
		{"one byte", []byte{0x00}},
		{"nonce only", valid[:12]},
		{"one short of the minimum", make([]byte, 27)},
		{"exactly the minimum, all zero", make([]byte, 28)},
		{"tag tampered", tampered},
		{"nonce tampered", flippedNonce},
		{"truncated ciphertext", valid[:len(valid)-1]},
		{"random garbage", randomBytes(t, 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A panic here is a crash in the DSN read path, i.e. an outage
			// per stored connection rather than a failed decrypt.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decrypt panicked on %s: %v", tc.name, r)
				}
			}()
			got, err := c.Decrypt(tc.blob)
			if err == nil {
				t.Fatalf("Decrypt(%s) returned %q, want an error", tc.name, got)
			}
			if got != "" {
				t.Errorf("plaintext = %q on failure, want empty", got)
			}
		})
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return b
}

func TestWireFormatIsNoncePlusSealed(t *testing.T) {
	// The layout is documented on DSNCipher and is what is already sitting in
	// db_connections rows, so it is a compatibility contract, not an
	// implementation detail: 12-byte nonce, then ciphertext + 16-byte tag.
	c := newCipher(t, testKey)
	const plain = "postgres://u:p@h:5432/d"

	blob, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if want := 12 + len(plain) + 16; len(blob) != want {
		t.Errorf("len(blob) = %d, want %d (12 nonce + %d plaintext + 16 tag)", len(blob), want, len(plain))
	}

	// A blob produced elsewhere with the same key must decrypt here — the
	// property that lets the key be rotated by re-encrypting rows, not by
	// re-deriving the format.
	fixed, err := hex.DecodeString(hex.EncodeToString(blob))
	if err != nil {
		t.Fatalf("hex round-trip: %v", err)
	}
	got, err := c.Decrypt(fixed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}
