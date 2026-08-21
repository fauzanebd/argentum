package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Two properties carry this whole file, and one of them is an outage if it
// breaks.
//
//  1. Every ciphertext already in every deployment is the prefix-free
//     `nonce||ciphertext` form. It has to keep opening forever.
//  2. A rotation must have a window: writes on the new key, reads on both,
//     and a way to tell when the re-seal is done.

const (
	keyA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyB = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	keyC = "1111111111111111111111111111111111111111111111111111111111111111"
)

// sealLegacy produces the pre-T-H14 wire format: nonce || ciphertext+tag, no
// prefix, no additional data. Written out longhand rather than by calling an
// old constructor, because the point is to pin the bytes that are *already in
// production databases* — a helper that shared code with the current
// implementation would drift with it and stop testing anything.
func sealLegacy(t *testing.T, hexKey, plain string) []byte {
	t.Helper()
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce, err := newNonce(aead.NonceSize())
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return append(nonce, aead.Seal(nil, nonce, []byte(plain), nil)...)
}

// The compatibility contract. If this fails, every stored DSN and every tenant
// credential in every deployment stops opening at once.
func TestLegacyCiphertextStillOpens(t *testing.T) {
	c, err := NewFromHex(keyA)
	if err != nil {
		t.Fatalf("NewFromHex: %v", err)
	}
	const plain = "postgres://u:p@h:5432/d"
	blob := sealLegacy(t, keyA, plain)

	if id, versioned := SealedUnder(blob); versioned {
		t.Errorf("a legacy blob reports as versioned under key %s", id)
	}
	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("a legacy ciphertext did not open: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}

// The rotation window, end to end: rows sealed under the old key keep opening
// while new rows are sealed under the new one.
func TestARotationWindowReadsBothKeys(t *testing.T) {
	const old, fresh = "sealed-before-rotation", "sealed-after-rotation"

	before, err := NewFromHex(keyA)
	if err != nil {
		t.Fatalf("NewFromHex: %v", err)
	}
	sealedUnderA, err := before.Encrypt(old)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	legacyUnderA := sealLegacy(t, keyA, old)

	// The rotation: B becomes primary, A is retired.
	after, err := NewKeyring(keyB, []string{keyA})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	for name, blob := range map[string][]byte{
		"versioned, old key": sealedUnderA,
		"legacy, old key":    legacyUnderA,
	} {
		got, err := after.Decrypt(blob)
		if err != nil {
			t.Errorf("%s did not open after rotation: %v", name, err)
		} else if got != old {
			t.Errorf("%s opened as %q, want %q", name, got, old)
		}
	}

	// New writes go to the new key, and say so.
	sealedUnderB, err := after.Encrypt(fresh)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	id, versioned := SealedUnder(sealedUnderB)
	if !versioned || id != after.PrimaryKeyID() {
		t.Errorf("new payload names key %q (versioned=%v), want the primary %q", id, versioned, after.PrimaryKeyID())
	}
	if id == before.PrimaryKeyID() {
		t.Error("new payload is still sealed under the retired key")
	}
}

// After the re-seal, the retired key can be dropped — and the check that it is
// safe to drop is exactly the one an operator runs.
func TestDroppingARetiredKeyBreaksOnlyUnsealedRows(t *testing.T) {
	before, _ := NewFromHex(keyA)
	stale, _ := before.Encrypt("not yet re-sealed")

	during, err := NewKeyring(keyB, []string{keyA})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	plain, err := during.Decrypt(stale)
	if err != nil {
		t.Fatalf("the retired key did not open a stale row: %v", err)
	}
	resealed, _ := during.Encrypt(plain)

	// The last step: drop the retired key.
	after, err := NewFromHex(keyB)
	if err != nil {
		t.Fatalf("NewFromHex: %v", err)
	}
	if _, err := after.Decrypt(resealed); err != nil {
		t.Errorf("a re-sealed row did not open after the retired key was dropped: %v", err)
	}
	// And the row that was missed fails in a way that names the cause.
	_, err = after.Decrypt(stale)
	if err == nil {
		t.Fatal("a row still on the retired key opened without it")
	}
	if !errors.Is(err, ErrNoKeyForBlob) {
		t.Errorf("error is %v, want ErrNoKeyForBlob — an operator fixes this by re-adding the key, not by restoring a backup", err)
	}
	if !strings.Contains(err.Error(), before.PrimaryKeyID()) {
		t.Errorf("error does not name the key the row needs: %v", err)
	}
}

// The fingerprint has to be stable and key-derived, or two processes holding
// the same key would disagree about what the rows say.
func TestKeyIDIsDerivedFromTheKey(t *testing.T) {
	a1, _ := NewFromHex(keyA)
	a2, _ := NewFromHex(keyA)
	b, _ := NewFromHex(keyB)

	if a1.PrimaryKeyID() != a2.PrimaryKeyID() {
		t.Error("two ciphers over the same key report different ids")
	}
	if a1.PrimaryKeyID() == b.PrimaryKeyID() {
		t.Error("two different keys share an id")
	}
	if len(a1.PrimaryKeyID()) != keyIDLen*2 {
		t.Errorf("key id is %q, want %d hex chars", a1.PrimaryKeyID(), keyIDLen*2)
	}
	// It must not be the key. Obvious, and worth an assertion because the
	// id is logged.
	if strings.Contains(keyA, a1.PrimaryKeyID()) {
		t.Error("the key id appears verbatim inside the key material")
	}
}

// The likeliest rotation mistake: setting the new key in both variables and
// believing the old one is still readable. Refused rather than deduplicated,
// because a silent dedup boots healthy and loses the rows it was configured to
// save.
func TestARetiredKeyEqualToThePrimaryIsRefused(t *testing.T) {
	_, err := NewKeyring(keyA, []string{keyA})
	if err == nil {
		t.Fatal("a keyring accepted the primary key as its own retired key")
	}
	if !strings.Contains(err.Error(), "already in the keyring") {
		t.Errorf("error does not explain the mistake: %v", err)
	}
}

// A retired key that will not parse is an error, not a warning: the whole point
// of configuring one is that some rows can only be read with it.
func TestAMalformedRetiredKeyRefusesToBoot(t *testing.T) {
	for _, bad := range []string{"nothex", "abcd"} {
		if _, err := NewKeyring(keyA, []string{bad}); err == nil {
			t.Errorf("NewKeyring accepted a malformed retired key %q", bad)
		}
	}
	// Blanks are skipped, so a trailing comma in the env var is not an outage.
	if _, err := NewKeyring(keyA, []string{"", "  "}); err != nil {
		t.Errorf("NewKeyring refused a list of blanks: %v", err)
	}
}

func TestSeveralRetiredKeysAreAllReadable(t *testing.T) {
	oldest, _ := NewFromHex(keyA)
	middle, _ := NewFromHex(keyB)
	fromA, _ := oldest.Encrypt("a")
	fromB, _ := middle.Encrypt("b")

	ring, err := NewKeyring(keyC, []string{keyA, keyB})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	if got, err := ring.Decrypt(fromA); err != nil || got != "a" {
		t.Errorf("oldest key: got %q, %v", got, err)
	}
	if got, err := ring.Decrypt(fromB); err != nil || got != "b" {
		t.Errorf("middle key: got %q, %v", got, err)
	}
	if ids := ring.RetiredKeyIDs(); len(ids) != 2 {
		t.Errorf("RetiredKeyIDs = %v, want two", ids)
	}
}

// The header is authenticated as additional data, so editing the key id or the
// version fails to open rather than opening as something else.
func TestTheHeaderIsAuthenticated(t *testing.T) {
	c, _ := NewFromHex(keyA)
	blob, err := c.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := make([]byte, len(blob))
	copy(tampered, blob)
	tampered[len(envelopeMagic)+1] ^= 0xff // flip a bit of the key id

	if _, err := c.Decrypt(tampered); err == nil {
		t.Error("a payload with an edited key id opened")
	}
}

// A ciphertext under a key nobody holds must fail, not silently return
// something. Both formats.
func TestAnUnknownKeyFails(t *testing.T) {
	stranger, _ := NewFromHex(keyB)
	versioned, _ := stranger.Encrypt("theirs")
	legacy := sealLegacy(t, keyB, "theirs")

	mine, _ := NewFromHex(keyA)
	if _, err := mine.Decrypt(versioned); err == nil {
		t.Error("a versioned payload under an unknown key opened")
	}
	if _, err := mine.Decrypt(legacy); err == nil {
		t.Error("a legacy payload under an unknown key opened")
	}
}
