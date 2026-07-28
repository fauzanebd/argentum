// Package webhookout delivers signed HTTP callbacks to a tenant's own server
// and records what it sent (T-A2).
//
// `POST /v1/reports` takes an optional `callback_url`, which makes Argentum an
// HTTP *client* against a customer's infrastructure for the first time. Three
// things have to exist before that is safe to offer:
//
//   - a way for the receiver to prove the body came from us (this file);
//   - a record on our side of what was sent and how it went, because
//     "we never got the callback" is otherwise an unanswerable support ticket;
//   - a refusal to be pointed at our own network, because the URL is chosen by
//     the caller and a blind POST to 169.254.169.254 is the classic way an
//     outbound webhook becomes someone else's credential.
//
// T-15 subscribes watcher events to this package rather than building a second
// sender, which is why nothing here names reports.
package webhookout

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the header carrying the timestamp and the MAC.
const SignatureHeader = "Argentum-Signature"

// EventHeader names the event, so a receiver can route without parsing the
// body it has not verified yet.
const EventHeader = "Argentum-Event"

// DeliveryHeader carries the delivery id. A tenant quoting it in a support
// conversation resolves it to the row that says what we sent.
const DeliveryHeader = "Argentum-Delivery"

// DefaultTolerance is how far out of date a signature may be and still verify.
// Five minutes: long enough to survive clock skew between two machines nobody
// synchronised, short enough that a captured request is not replayable for the
// rest of the day.
const DefaultTolerance = 5 * time.Minute

// Sign renders the `Argentum-Signature` value for a body at a point in time.
//
// The signed message is `<unix timestamp>.<raw body>`, not the body alone.
// Without the timestamp inside the MAC, an attacker who captured one delivery
// could replay it forever with a fresh `t=` — the receiver would check a
// timestamp nothing had authenticated. This is the same construction Stripe
// and GitHub use, deliberately: an integrator who has verified one of those
// has already written this code.
func Sign(secret string, at time.Time, body []byte) string {
	ts := strconv.FormatInt(at.UTC().Unix(), 10)
	return "t=" + ts + ",v1=" + mac(secret, ts, body)
}

func mac(secret, ts string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(ts))
	h.Write([]byte{'.'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Verify checks a header against a body. It is the receiver's half, and it
// lives here rather than only in the quickstart so that the property the
// acceptance criterion names — a tampered body does not verify — is tested
// against the implementation that produces the header rather than against a
// second reading of the prose.
//
// tolerance <= 0 uses DefaultTolerance.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	if tolerance <= 0 {
		tolerance = DefaultTolerance
	}
	ts, sig, err := parseSignature(header)
	if err != nil {
		return err
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("signature timestamp is not a unix time")
	}
	skew := now.UTC().Sub(time.Unix(secs, 0).UTC())
	if skew < 0 {
		skew = -skew
	}
	if skew > tolerance {
		return fmt.Errorf("signature is %s out of date", skew.Round(time.Second))
	}
	want := mac(secret, ts, body)
	// hmac.Equal, never ==. A byte-by-byte string comparison returns early on
	// the first difference, which leaks the position of the mismatch through
	// timing and lets a signature be recovered one byte at a time.
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return fmt.Errorf("signature does not match the body")
	}
	return nil
}

// parseSignature reads `t=…,v1=…` in either order and ignores unknown pairs,
// so a future `v2=` can be added without every existing receiver breaking —
// which is the only reason the scheme is versioned at all.
func parseSignature(header string) (ts, sig string, err error) {
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	if ts == "" || sig == "" {
		return "", "", fmt.Errorf("signature header must carry t= and v1=")
	}
	return ts, sig, nil
}
