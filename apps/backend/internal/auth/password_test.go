package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func b64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

func TestHashPasswordRoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"ascii", "correct horse battery staple"},
		{"short", "a"},
		{"unicode", "kata-sandi-âñ-日本語-🔐"},
		{"with a dollar sign", "pa$$word$argon2id$"},
		{"long", strings.Repeat("x", 1024)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := HashPassword(tc.password)
			if err != nil {
				t.Fatalf("HashPassword: %v", err)
			}
			// The stored string must not be the password. Checked only for
			// passwords long enough that a substring hit means something —
			// a one-character password appears in any base64 by chance.
			if len(tc.password) >= 8 && strings.Contains(encoded, tc.password) {
				t.Error("the encoded hash contains the plaintext password")
			}

			ok, err := VerifyPassword(tc.password, encoded)
			if err != nil {
				t.Fatalf("VerifyPassword: %v", err)
			}
			if !ok {
				t.Error("the correct password did not verify")
			}

			ok, err = VerifyPassword(tc.password+"x", encoded)
			if err != nil {
				t.Fatalf("VerifyPassword with a wrong password returned an error: %v", err)
			}
			if ok {
				t.Error("a wrong password verified")
			}
		})
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	// An empty password that hashed successfully would let an account be
	// created with no credential at all.
	if _, err := HashPassword(""); err == nil {
		t.Fatal("HashPassword(\"\") = nil error, want an error")
	}
}

func TestHashPasswordSaltsEachCall(t *testing.T) {
	// Two accounts sharing a password must not share a hash, or the database
	// leaks which users picked the same one.
	const pw = "same password"
	first, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of one password are identical: the salt is not random")
	}

	// Both still verify — a different salt is not a different password.
	for i, enc := range []string{first, second} {
		ok, err := VerifyPassword(pw, enc)
		if err != nil || !ok {
			t.Errorf("hash %d: ok=%v err=%v, want true/nil", i, ok, err)
		}
	}
}

func TestHashPasswordEncodesPHCFormat(t *testing.T) {
	// The prefix is what VerifyPassword parses and what a future parameter
	// bump has to stay compatible with, so it is a stored-format contract.
	encoded, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("encoded hash has %d $-separated parts, want 6: %q", len(parts), encoded)
	}
	if parts[0] != "" {
		t.Errorf("encoded hash does not start with $: %q", encoded)
	}
	if parts[1] != "argon2id" {
		t.Errorf("algorithm = %q, want argon2id", parts[1])
	}
	if want := fmt.Sprintf("v=%d", argon2.Version); parts[2] != want {
		t.Errorf("version segment = %q, want %q", parts[2], want)
	}
	wantParams := fmt.Sprintf("m=%d,t=%d,p=%d",
		defaultParams.Memory, defaultParams.Iterations, defaultParams.Parallelism)
	if parts[3] != wantParams {
		t.Errorf("parameter segment = %q, want %q", parts[3], wantParams)
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	// password_hash is a text column. A corrupted or foreign value must come
	// back as an error, never as a panic and never as a successful match.
	valid, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(valid, "$")

	cases := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"not a hash", "hunter2"},
		{"too few segments", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA"},
		{"too many segments", valid + "$extra"},
		{"wrong algorithm", strings.Replace(valid, "argon2id", "argon2i", 1)},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"unparseable version", "$argon2id$vNINETEEN$" + parts[3] + "$" + parts[4] + "$" + parts[5]},
		{"incompatible version", "$argon2id$v=16$" + parts[3] + "$" + parts[4] + "$" + parts[5]},
		{"unparseable params", "$argon2id$" + parts[2] + "$m=lots,t=3,p=2$" + parts[4] + "$" + parts[5]},
		{"salt is not base64", "$argon2id$" + parts[2] + "$" + parts[3] + "$!!!!$" + parts[5]},
		{"hash is not base64", "$argon2id$" + parts[2] + "$" + parts[3] + "$" + parts[4] + "$!!!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("VerifyPassword panicked on %s: %v", tc.name, r)
				}
			}()
			ok, err := VerifyPassword("hunter2", tc.encoded)
			if ok {
				t.Errorf("VerifyPassword(%s) = true, want false", tc.name)
			}
			if err == nil {
				t.Errorf("VerifyPassword(%s) returned no error for a malformed hash", tc.name)
			}
		})
	}
}

func TestVerifyPasswordHonoursTheStoredParameters(t *testing.T) {
	// The cost parameters are read back out of the stored string rather than
	// taken from defaultParams, which is what lets the defaults be raised
	// later without invalidating every existing password.
	const pw = "hunter2"
	weaker := argon2Params{Memory: 32 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

	encoded := encodeWith(t, pw, weaker)
	ok, err := VerifyPassword(pw, encoded)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("a hash stored under older parameters no longer verifies")
	}

	// And the parameters actually participate: the same salt under the
	// current defaults produces a different key.
	if weaker.Iterations == defaultParams.Iterations && weaker.Memory == defaultParams.Memory {
		t.Skip("the weaker parameter set is no longer weaker than the defaults")
	}
	ok, err = VerifyPassword("wrong", encoded)
	if err != nil || ok {
		t.Errorf("wrong password under stored parameters: ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestVerifyPasswordAcceptsAShorterStoredKey(t *testing.T) {
	// KeyLength is not in the PHC parameter segment, so it is inferred from
	// the decoded hash. A stored 16-byte key must verify against a 16-byte
	// derivation, not be compared against a 32-byte one and always fail.
	const pw = "hunter2"
	shortKey := defaultParams
	shortKey.KeyLength = 16

	encoded := encodeWith(t, pw, shortKey)
	ok, err := VerifyPassword(pw, encoded)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("a hash with a 16-byte key did not verify")
	}
}

// encodeWith produces a PHC string using an arbitrary parameter set, standing
// in for a row written by an older build.
func encodeWith(t *testing.T, password string, p argon2Params) string {
	t.Helper()
	salt := []byte("0123456789abcdef")[:p.SaltLength]
	hash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		b64(salt), b64(hash))
}
