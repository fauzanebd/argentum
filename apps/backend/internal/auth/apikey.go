package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// API key tokens (T-13).
//
// Shape: `arg_<prefix>_<secret>`.
//
//   - `arg` is a fixed marker. It exists so a leaked key is recognisable as
//     an Argentum credential in a log, a paste or a secret scanner, which is
//     what makes an automated revoke possible at all.
//   - `<prefix>` is 10 hex characters over 5 random bytes. It is public: it
//     is stored in the clear, shown in the dashboard, and is the column
//     authentication looks the key up by.
//   - `<secret>` is 32 bytes of crypto/rand in base64url. Only its hash is
//     stored.
//
// **Why the secret is hashed with SHA-256 and not the Argon2id the ticket
// names.** Argon2id exists to make guessing expensive against a low-entropy
// input — a password. This secret is 256 uniformly random bits: there is no
// dictionary, and an attacker who can brute-force it can brute-force the
// Argon2id variant too, because the search space is the whole point rather
// than the KDF. What the KDF would cost is real and lands in the wrong place:
// ~64 MiB and ~50ms per *authenticated request* on a machine-to-machine API,
// where a login pays it once a fortnight. It is also an amplification vector
// on the unauthenticated path — anyone holding a valid prefix could make the
// server allocate 64 MiB per wrong-secret guess. The threat this hash defends
// against is a dump of `api_keys`, and a SHA-256 of a 256-bit random value is
// not reversible. This deviation is recorded in docs/coverage/api-keys.md.
//
// Argon2id stays where it belongs: HashPassword, in password.go, for the
// input a human chose.
const (
	// apiKeyMarker is the leading segment of every token.
	apiKeyMarker = "arg"
	// apiKeyPrefixBytes is the entropy behind the public half. It is an
	// identifier, not a secret — 40 bits is about collision resistance across
	// a tenant's keys, and the UNIQUE constraint is what actually enforces it.
	apiKeyPrefixBytes = 5
	// apiKeySecretBytes matches the invite token: 32 bytes is the budget for
	// a bearer credential that opens a company's data.
	apiKeySecretBytes = 32
)

// NewAPIKey mints a token and returns it alongside the two halves that get
// stored. The token is the only time the secret exists outside the caller's
// memory; nothing persists it and nothing can reconstruct it.
func NewAPIKey() (token, prefix, hash string, err error) {
	pbuf := make([]byte, apiKeyPrefixBytes)
	if _, err := rand.Read(pbuf); err != nil {
		return "", "", "", err
	}
	sbuf := make([]byte, apiKeySecretBytes)
	if _, err := rand.Read(sbuf); err != nil {
		return "", "", "", err
	}
	prefix = hex.EncodeToString(pbuf)
	secret := base64.RawURLEncoding.EncodeToString(sbuf)
	return apiKeyMarker + "_" + prefix + "_" + secret, prefix, HashAPIKeySecret(secret), nil
}

// ParseAPIKey splits a presented token into its public and secret halves. It
// validates shape only — that the marker is ours and both halves are
// non-empty — so that a malformed Authorization header costs a string split
// rather than a database round trip.
//
// The split is bounded at three parts because base64url includes `_`: the
// secret may contain underscores and must not be truncated at the first one.
func ParseAPIKey(token string) (prefix, secret string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(token), "_", 3)
	if len(parts) != 3 || parts[0] != apiKeyMarker || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// HashAPIKeySecret is what gets stored in api_keys.key_hash.
func HashAPIKeySecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// APIKeySecretMatches compares a presented secret against a stored hash in
// constant time. The comparison is over the hex encodings, which are fixed
// length for every well-formed row, so a mismatch reveals nothing through
// timing even when the stored value is corrupt.
func APIKeySecretMatches(secret, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashAPIKeySecret(secret)), []byte(storedHash)) == 1
}
