package webhookout

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

const testSecret = "whsec_0123456789abcdefghijklmnopqrstuv"

func TestSignedBodyVerifies(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"event":"report.completed","data":{"id":"rep_1"}}`)

	header := Sign(testSecret, now, body)
	if err := Verify(testSecret, header, body, now, 0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// The acceptance criterion, stated as a test: a body that changed does not
// verify. One byte, in the middle, so this is not passing because the length
// changed.
func TestTamperedBodyDoesNotVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"amount":100}`)
	header := Sign(testSecret, now, body)

	tampered := []byte(`{"amount":900}`)
	if err := Verify(testSecret, header, tampered, now, 0); err == nil {
		t.Fatal("a tampered body verified against the original signature")
	}
}

func TestWrongSecretDoesNotVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"a":1}`)
	header := Sign(testSecret, now, body)

	if err := Verify("whsec_someone_elses_secret", header, body, now, 0); err == nil {
		t.Fatal("a signature verified under the wrong secret")
	}
}

// The timestamp is inside the MAC, so a captured delivery cannot be replayed
// with a fresh `t=`. Both halves are checked: an old signature is refused for
// age, and a re-dated one is refused because the MAC no longer matches.
func TestReplayIsRefused(t *testing.T) {
	signedAt := time.Unix(1_800_000_000, 0)
	body := []byte(`{"a":1}`)
	header := Sign(testSecret, signedAt, body)

	later := signedAt.Add(time.Hour)
	if err := Verify(testSecret, header, body, later, DefaultTolerance); err == nil {
		t.Fatal("an hour-old signature verified inside a five-minute tolerance")
	}

	// Re-date it and keep the original MAC — the shape of an actual replay.
	_, sig, err := parseSignature(header)
	if err != nil {
		t.Fatalf("parseSignature: %v", err)
	}
	forged := "t=" + strconv.FormatInt(later.Unix(), 10) + ",v1=" + sig
	if err := Verify(testSecret, forged, body, later, DefaultTolerance); err == nil {
		t.Fatal("a re-dated signature verified; the timestamp is not inside the MAC")
	}
}

// Clock skew in either direction is tolerated: two machines nobody
// synchronised will disagree, and a receiver whose clock runs fast must not
// reject a signature that has not been issued yet by its own reckoning.
func TestSkewIsToleratedInBothDirections(t *testing.T) {
	signedAt := time.Unix(1_800_000_000, 0)
	body := []byte(`{"a":1}`)
	header := Sign(testSecret, signedAt, body)

	for _, skew := range []time.Duration{-2 * time.Minute, 2 * time.Minute} {
		if err := Verify(testSecret, header, body, signedAt.Add(skew), DefaultTolerance); err != nil {
			t.Errorf("skew %s: %v", skew, err)
		}
	}
}

// The header is versioned so a `v2=` can be added without breaking every
// existing receiver, which only works if unknown pairs are ignored and order
// is not assumed.
func TestSignatureHeaderParsesLoosely(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"a":1}`)
	header := Sign(testSecret, now, body)
	ts, sig, err := parseSignature(header)
	if err != nil {
		t.Fatalf("parseSignature: %v", err)
	}

	for _, variant := range []string{
		"v1=" + sig + ",t=" + ts,
		"t=" + ts + ", v1=" + sig,
		"t=" + ts + ",v1=" + sig + ",v2=deadbeef",
	} {
		if err := Verify(testSecret, variant, body, now, 0); err != nil {
			t.Errorf("Verify(%q): %v", variant, err)
		}
	}

	for _, bad := range []string{"", "t=123", "v1=abc", "garbage"} {
		if err := Verify(testSecret, bad, body, now, 0); err == nil {
			t.Errorf("Verify accepted a malformed header %q", bad)
		}
	}
}

func TestSignatureShape(t *testing.T) {
	header := Sign(testSecret, time.Unix(1_800_000_000, 0), []byte(`{}`))
	if !strings.HasPrefix(header, "t=1800000000,v1=") {
		t.Fatalf("header = %q, want t=<unix>,v1=<hex>", header)
	}
	if _, sig, _ := parseSignature(header); len(sig) != 64 {
		t.Errorf("v1 is %d hex chars, want 64 for SHA-256", len(sig))
	}
}
