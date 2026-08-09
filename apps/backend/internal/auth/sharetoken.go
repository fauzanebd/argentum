package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Share tokens (T-V4) — the credential in a report player's URL.
//
// Shape: 43 base64url characters over 32 random bytes, with no marker and no
// public prefix. Both omissions are deliberate and both differ from
// `NewAPIKey` above:
//
//   - **No marker.** An API key wants to be recognisable in a log so a secret
//     scanner can revoke it. A share token travels in a URL that a tenant
//     pastes into an email, and a recognisable prefix is what makes it worth
//     grepping for in the first place.
//   - **No public prefix.** A key is looked up by prefix and then verified;
//     a share is looked up by the hash of the whole token, in one indexed
//     read. There is nothing to show in a UI afterwards, because there is
//     nothing about a share worth identifying except the row we already have.
//
// The hash is SHA-256 for the argument `HashAPIKeySecret` makes at length: the
// input is 256 uniformly random bits, so a KDF slows down a dictionary that
// does not exist while costing 64 MiB on every page view of a public URL —
// which is a denial-of-service handed to anyone who can type a wrong token.
const shareTokenBytes = 32

// NewShareToken mints a token and the hash to store. The token is returned
// exactly once and nothing can reconstruct it from the row.
func NewShareToken() (token, hash string, err error) {
	buf := make([]byte, shareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashShareToken(token), nil
}

// HashShareToken is what report_shares.token_hash holds.
//
// No constant-time comparison anywhere in this flow, and that is not an
// oversight: the lookup is `WHERE token_hash = $1` on an indexed column, so
// nothing ever compares a presented secret against a stored one in Go. The
// database finds the row or it does not.
func HashShareToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}
