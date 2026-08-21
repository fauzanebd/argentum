package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Key rotation (T-H14).
//
// **The problem this closes.** One `ARGENTUM_DSN_KEY` seals every DSN and every
// tenant credential, and until now there was no rotation path at all: changing
// the key meant every stored ciphertext stopped opening, at once, discovered by
// an agent telling a customer *"there appears to be a decryption problem with
// the database connection string"* mid-turn. That is not hypothetical — three
// distinct keys existed on this project inside a fortnight and two of twenty
// stored connections open under none of them (`app.LogDSNKeyCoverage`, and
// live-gate-backlog §1b).
//
// **What makes rotation possible is reading with more than one key.** A cutover
// where writes move to a new key and reads still accept the old one turns a
// hard swap into a window: set the new key primary, keep the old one retired,
// re-seal the rows, confirm the sweep reports zero rows under the old key, then
// drop it. Every step is verifiable and every step is reversible until the last.
//
// **And what makes it verifiable is the version prefix**, which the ticket
// names explicitly — "the cipher has no version field today and adding one is
// part of this ticket". Without it, "which key is this row sealed under?" can
// only be answered by trying them all, so "is the rotation finished?" cannot be
// answered at all.
//
// **Backward compatibility is not optional here.** Every ciphertext already in
// every deployment is the legacy `nonce||ciphertext` form with no prefix. It has
// to keep opening forever, or this file is the outage it exists to prevent —
// see [DSNCipher.Decrypt].
//
// **What is still open.** The ticket also asks for envelope encryption with
// per-tenant data keys, which is what "do you support customer-managed keys"
// really wants. That needs a company id at every call site — `Encrypt(string)`
// has no tenant in its signature and ten packages call it — plus a decision
// about which KMS, which is an operator's rather than an implementer's. The
// keyring is the half that had to come first either way: a per-tenant data key
// is a key that must be findable by id and re-sealable under a new master,
// which is exactly what [keyID] and the version prefix make possible.

// Wire format, version 1:
//
//	"ARGK" | 0x01 | keyID[4] | nonce[12] | ciphertext+tag
//
// Legacy (everything written before this file):
//
//	nonce[12] | ciphertext+tag
const (
	envelopeMagic   = "ARGK"
	envelopeVersion = byte(0x01)
	keyIDLen        = 4
	envelopeHeader  = len(envelopeMagic) + 1 + keyIDLen
)

// ErrNoKeyForBlob is returned when a versioned payload names a key this process
// does not hold. Distinct from a decrypt failure on purpose: it means "the key
// that seals this row was not configured", which an operator fixes by adding a
// retired key rather than by restoring a backup.
var ErrNoKeyForBlob = errors.New("no configured key matches this ciphertext")

// DSNCipher seals and opens payloads using AES-256-GCM under a keyring.
//
// The name and the two methods are unchanged from the single-key version this
// replaces, which is why ten call sites did not have to move.
type DSNCipher struct {
	// primary seals. Exactly one, always.
	primary keyEntry
	// retired open and never seal. Ordered as configured, tried in order.
	retired []keyEntry
}

type keyEntry struct {
	id   string
	aead cipher.AEAD
}

// NewFromHex parses a 64-char hex-encoded 32-byte key and returns a keyring
// holding only it. The historical constructor, kept because most callers and
// every test want one key.
func NewFromHex(hexKey string) (*DSNCipher, error) {
	return NewKeyring(hexKey, nil)
}

// NewKeyring builds a cipher that seals under primaryHex and opens under
// primaryHex plus every key in retiredHex.
//
// A retired key that fails to parse is an error rather than a warning. The
// whole point of configuring one is that some rows can only be read with it,
// so a deployment that silently dropped it would boot healthy and fail on the
// tenants it was added for.
func NewKeyring(primaryHex string, retiredHex []string) (*DSNCipher, error) {
	primary, err := parseKey(primaryHex)
	if err != nil {
		return nil, err
	}
	c := &DSNCipher{primary: primary}
	seen := map[string]bool{primary.id: true}
	for i, h := range retiredHex {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		k, err := parseKey(h)
		if err != nil {
			return nil, fmt.Errorf("retired key %d: %w", i+1, err)
		}
		// A retired key equal to the primary is configuration that reads as a
		// rotation in progress and is not one. Refused rather than deduplicated
		// silently, because the likeliest cause is an operator who set the new
		// key in both variables and believes the old one is still readable.
		if seen[k.id] {
			return nil, fmt.Errorf("retired key %d is already in the keyring (id %s); "+
				"a retired key must be a key the primary replaced", i+1, k.id)
		}
		seen[k.id] = true
		c.retired = append(c.retired, k)
	}
	return c, nil
}

func parseKey(hexKey string) (keyEntry, error) {
	if hexKey == "" {
		return keyEntry{}, errors.New("ARGENTUM_DSN_KEY is required")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return keyEntry{}, fmt.Errorf("decode hex key: %w", err)
	}
	if len(key) != 32 {
		return keyEntry{}, fmt.Errorf("expected 32-byte key, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return keyEntry{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keyEntry{}, err
	}
	return keyEntry{id: keyID(key), aead: aead}, nil
}

// keyID is the first four bytes of SHA-256 over the key material, hex-encoded.
//
// A fingerprint rather than a counter, because a counter has to be assigned by
// somebody and two deployments would assign different numbers to the same key.
// A fingerprint is the same everywhere and is derivable from the key alone,
// which is what lets an operator check that the value in a log line is the key
// they think they configured.
//
// It is a *truncated* hash of a secret and is safe to log: eight hex characters
// of SHA-256 identify the key among the two or three a deployment holds and
// carry no usable information about its bytes.
func keyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:keyIDLen])
}

// PrimaryKeyID is the fingerprint of the key that seals new payloads. Logged at
// boot and reported by the key-health sweep, so "which key is this process
// writing under" is answerable without guessing.
func (c *DSNCipher) PrimaryKeyID() string { return c.primary.id }

// RetiredKeyIDs are the fingerprints this process can still read but will never
// write. Empty on a deployment that has not rotated.
func (c *DSNCipher) RetiredKeyIDs() []string {
	out := make([]string, 0, len(c.retired))
	for _, k := range c.retired {
		out = append(out, k.id)
	}
	return out
}

// Encrypt seals plaintext under the primary key, in the versioned format.
func (c *DSNCipher) Encrypt(plain string) ([]byte, error) {
	nonce, err := newNonce(c.primary.aead.NonceSize())
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, envelopeHeader+len(nonce)+len(plain)+c.primary.aead.Overhead())
	out = append(out, envelopeMagic...)
	out = append(out, envelopeVersion)
	id, err := hex.DecodeString(c.primary.id)
	if err != nil {
		return nil, fmt.Errorf("key id: %w", err)
	}
	out = append(out, id...)
	out = append(out, nonce...)
	// The header is authenticated as additional data, so a payload whose key id
	// or version was edited fails to open rather than opening as something
	// else. Without it the prefix is unauthenticated metadata on an
	// authenticated payload, which is the shape that invites confusion attacks.
	return c.primary.aead.Seal(out, nonce, []byte(plain), out[:envelopeHeader]), nil
}

// Decrypt opens a payload written in either format.
//
// **The legacy branch is permanent.** Every ciphertext in every deployment
// predating this file has no prefix, and there is no migration that can be
// relied upon to have run everywhere — so a reader that required the prefix
// would be the outage this whole file exists to prevent. `cmd/rekey` re-seals
// rows into the new format; nothing requires it to have finished.
//
// The two formats are told apart by the magic, and a legacy nonce beginning
// with those exact five bytes is a 1-in-2^40 accident. It is still handled: a
// versioned parse that fails falls through to the legacy attempt rather than
// returning, so the worst case for that accident is a slower decrypt.
func (c *DSNCipher) Decrypt(blob []byte) (string, error) {
	if id, nonce, ct, ok := parseEnvelope(blob); ok {
		if k, found := c.byID(id); found {
			if plain, err := k.aead.Open(nil, nonce, ct, blob[:envelopeHeader]); err == nil {
				return string(plain), nil
			}
		} else if !c.legacyOpens(blob) {
			// Named a key nobody holds, and it is not a legacy blob wearing the
			// magic by accident. The distinct error is the point: an operator
			// fixes this by configuring a retired key, not by restoring a backup.
			return "", fmt.Errorf("%w: sealed under key %s; configured keys are %s",
				ErrNoKeyForBlob, id, strings.Join(append([]string{c.primary.id}, c.RetiredKeyIDs()...), ", "))
		}
	}
	return c.decryptLegacy(blob)
}

// decryptLegacy opens the prefix-free format, trying the primary first and then
// each retired key in configured order.
func (c *DSNCipher) decryptLegacy(blob []byte) (string, error) {
	var firstErr error
	for _, k := range append([]keyEntry{c.primary}, c.retired...) {
		ns := k.aead.NonceSize()
		if len(blob) < ns+k.aead.Overhead() {
			if firstErr == nil {
				firstErr = errors.New("ciphertext too short")
			}
			continue
		}
		plain, err := k.aead.Open(nil, blob[:ns], blob[ns:], nil)
		if err == nil {
			return string(plain), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = errors.New("ciphertext too short")
	}
	return "", fmt.Errorf("decrypt: %w", firstErr)
}

func (c *DSNCipher) legacyOpens(blob []byte) bool {
	_, err := c.decryptLegacy(blob)
	return err == nil
}

func (c *DSNCipher) byID(id string) (keyEntry, bool) {
	if c.primary.id == id {
		return c.primary, true
	}
	for _, k := range c.retired {
		if k.id == id {
			return k, true
		}
	}
	return keyEntry{}, false
}

// SealedUnder reports which key a payload names, and whether it carries a
// version prefix at all.
//
// This is the function that makes a rotation finishable. "Re-seal every row"
// is only actionable if "which rows are still on the old key" has an answer,
// and before the prefix existed it did not: a row sealed under a key this
// process holds is indistinguishable from one sealed under the primary, so the
// sweep could report failures and never progress.
//
// A legacy payload returns ("", false) — it names no key, which is exactly what
// makes it a row to re-seal.
func SealedUnder(blob []byte) (string, bool) {
	id, _, _, ok := parseEnvelope(blob)
	if !ok {
		return "", false
	}
	return id, true
}

// parseEnvelope splits a versioned payload. ok is false for anything else,
// including a truncated one — a caller that got a short read should fall
// through to the legacy attempt rather than report a key that is not there.
func parseEnvelope(blob []byte) (id string, nonce, ct []byte, ok bool) {
	const nonceLen = 12
	if len(blob) < envelopeHeader+nonceLen {
		return "", nil, nil, false
	}
	if string(blob[:len(envelopeMagic)]) != envelopeMagic {
		return "", nil, nil, false
	}
	if blob[len(envelopeMagic)] != envelopeVersion {
		return "", nil, nil, false
	}
	id = hex.EncodeToString(blob[len(envelopeMagic)+1 : envelopeHeader])
	nonce = blob[envelopeHeader : envelopeHeader+nonceLen]
	ct = blob[envelopeHeader+nonceLen:]
	return id, nonce, ct, true
}
