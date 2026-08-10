package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Embed key material (T-19).
//
// Two halves, and they are used in opposite directions from an API key's:
//
//   - `argw_pub_<hex>` is the **client key**. It is public — printed in the
//     tenant's page source — and it identifies a key without authorising
//     anything.
//   - The **signing secret** never reaches a browser. The tenant's *backend*
//     holds it and computes `HMAC-SHA256(secret, "<user_ref>:<exp>")` to
//     assert who their visitor is. We hold it too, sealed with the DSN cipher,
//     because verifying an HMAC requires the key rather than a hash of it — see
//     the 051 migration for why that deviation from the ticket is the only
//     jointly-satisfiable reading of it.
//
// The marker is `argw` rather than `arg` on purpose. A secret scanner, a log
// grep and a human reading a paste all need to tell a server-side credential
// from a browser-side one at a glance, and the two have very different
// blast radii — one reaches a tenant's whole warehouse, the other mints a
// 15-minute session for one asserted user.
const (
	// embedKeyMarker prefixes the public half. `pub` is spelled out because
	// this string will be pasted into public HTML, and the next person to read
	// it should not have to ask whether it is a secret.
	embedKeyMarker = "argw_pub"
	// embedClientKeyBytes backs the public half. An identifier, not a secret:
	// 16 bytes is about collision resistance, and the UNIQUE constraint is what
	// actually enforces it.
	embedClientKeyBytes = 16
	// embedSecretBytes is the HMAC key. 32 bytes matches the API key secret and
	// the invite token — the budget for a credential that can assert identity.
	embedSecretBytes = 32
)

// NewEmbedKey mints a client key and its signing secret. The secret is returned
// exactly once, to be shown to the admin and then sealed; nothing can
// reconstruct it from the client key.
func NewEmbedKey() (clientKey, secret string, err error) {
	cbuf := make([]byte, embedClientKeyBytes)
	if _, err := rand.Read(cbuf); err != nil {
		return "", "", err
	}
	sbuf := make([]byte, embedSecretBytes)
	if _, err := rand.Read(sbuf); err != nil {
		return "", "", err
	}
	return embedKeyMarker + "_" + hex.EncodeToString(cbuf), hex.EncodeToString(sbuf), nil
}

// ValidEmbedClientKey reports whether raw is shaped like one of ours. Shape
// only, so a malformed value costs a string comparison rather than a database
// round trip.
func ValidEmbedClientKey(raw string) bool {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, embedKeyMarker+"_") {
		return false
	}
	body := strings.TrimPrefix(s, embedKeyMarker+"_")
	if len(body) != embedClientKeyBytes*2 {
		return false
	}
	_, err := hex.DecodeString(body)
	return err == nil
}

// EmbedSignature computes the signature a tenant's backend is expected to send:
// HMAC-SHA256 over `<user_ref>:<exp>`, hex-encoded.
//
// The signed string is the identity *and* the deadline together. Signing only
// the user_ref would let a page replay last year's signature forever; signing
// only the exp would let any signature stand in for any user.
func EmbedSignature(secret, userRef string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(userRef + ":" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// EmbedSignatureValid compares a presented signature against the expected one
// in constant time.
//
// **`hmac.Equal`, never `==`.** A byte-by-byte string comparison returns as
// soon as it finds a difference, so the time it takes leaks how many leading
// characters were right — and a signature is exactly the kind of value an
// attacker can submit a few million of. The ticket asks for this in as many
// words and its gate greps the diff for it.
func EmbedSignatureValid(secret, userRef string, exp int64, presented string) bool {
	want := EmbedSignature(secret, userRef, exp)
	return hmac.Equal([]byte(want), []byte(strings.TrimSpace(presented)))
}
