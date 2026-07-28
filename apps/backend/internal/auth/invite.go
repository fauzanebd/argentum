package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// inviteTokenBytes is the entropy behind an invite link. 32 bytes is the same
// budget as a session key: the token is a bearer credential that activates an
// account, and it travels through email, so it has to survive being guessed by
// anyone who knows the URL shape.
const inviteTokenBytes = 32

// NewInviteToken returns a URL-safe token and the hash to store for it. The
// plaintext is returned to the caller once and never persisted, so a dump of
// user_invites cannot be replayed into account takeovers.
func NewInviteToken() (token, hash string, err error) {
	buf := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashInviteToken(token), nil
}

// HashInviteToken is a plain SHA-256, not Argon2id: the token is 256 bits of
// uniform randomness, so there is no dictionary to slow an attacker down
// against, and lookup happens on every accept request.
func HashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
