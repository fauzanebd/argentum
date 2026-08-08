package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

const testSecret = "8f742231b10e8888abcd99yyyzzz85a5"

// sign reproduces Slack's documented v0 recipe so the tests exercise the
// verifier against an independently built signature.
func sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature_valid(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)

	if err := VerifySignature(testSecret, ts, sign(testSecret, ts, body), body, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifySignature_knownVector(t *testing.T) {
	// Slack's published example from the "Verifying requests" docs.
	const (
		secret = "8f742231b10e8888abcd99yyyzzz85a5"
		ts     = "1531420618"
		body   = "token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow" +
			"&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner" +
			"&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2F" +
			"commands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=" +
			"398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c"
		want = "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503"
	)
	now := time.Unix(1531420618, 0)
	if err := VerifySignature(secret, ts, want, []byte(body), now); err != nil {
		t.Fatalf("known-good Slack vector rejected: %v", err)
	}
}

func TestVerifySignature_tamperedBody(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	sig := sign(testSecret, ts, body)

	if err := VerifySignature(testSecret, ts, sig, []byte(`{"type":"evil"}`), now); err == nil {
		t.Fatal("tampered body accepted")
	}
}

func TestVerifySignature_wrongSecret(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{}`)

	if err := VerifySignature("other-secret", ts, sign(testSecret, ts, body), body, now); err == nil {
		t.Fatal("signature from a different secret accepted")
	}
}

func TestVerifySignature_replayOutsideWindow(t *testing.T) {
	signedAt := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(signedAt.Unix(), 10)
	body := []byte(`{}`)
	sig := sign(testSecret, ts, body)

	// Same signature, replayed six minutes later.
	if err := VerifySignature(testSecret, ts, sig, body, signedAt.Add(6*time.Minute)); err == nil {
		t.Fatal("stale timestamp accepted")
	}
	// Still fine just inside the window.
	if err := VerifySignature(testSecret, ts, sig, body, signedAt.Add(4*time.Minute)); err != nil {
		t.Fatalf("timestamp inside window rejected: %v", err)
	}
	// Clock skew in the other direction is bounded too.
	if err := VerifySignature(testSecret, ts, sig, body, signedAt.Add(-6*time.Minute)); err == nil {
		t.Fatal("future timestamp accepted")
	}
}

func TestVerifySignature_missingInputs(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{}`)
	sig := sign(testSecret, ts, body)

	cases := []struct {
		name              string
		secret, ts, sigIn string
	}{
		{"no secret", "", ts, sig},
		{"no signature", testSecret, ts, ""},
		{"no timestamp", testSecret, "", sig},
		{"non-numeric timestamp", testSecret, "not-a-number", sig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifySignature(tc.secret, tc.ts, tc.sigIn, body, now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
