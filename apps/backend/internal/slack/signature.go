package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// MaxTimestampSkew bounds how old an X-Slack-Request-Timestamp may be
// before the request is rejected as a replay. Slack's own guidance is five
// minutes.
const MaxTimestampSkew = 5 * time.Minute

// signatureVersion is the only version prefix Slack has shipped so far.
const signatureVersion = "v0"

// VerifySignature validates the X-Slack-Signature header. Slack computes it
// as `v0=hex(hmac_sha256(signing_secret, "v0:" + timestamp + ":" + body))`.
// The timestamp is checked against now to bound replays; pass the raw,
// unparsed request body — re-marshalled JSON will not match.
func VerifySignature(signingSecret, timestamp, signature string, body []byte, now time.Time) error {
	if signingSecret == "" {
		return errors.New("signing_secret required for signature verification")
	}
	if signature == "" {
		return errors.New("missing signature")
	}
	if err := checkTimestamp(timestamp, now); err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(signatureVersion + ":" + timestamp + ":"))
	mac.Write(body)
	want := signatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))

	// Constant-time compare over the whole "v0=..." string.
	if !hmac.Equal([]byte(signature), []byte(want)) {
		return errors.New("signature mismatch")
	}
	return nil
}

func checkTimestamp(timestamp string, now time.Time) error {
	if timestamp == "" {
		return errors.New("missing timestamp")
	}
	secs, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp %q: %w", timestamp, err)
	}
	skew := now.Sub(time.Unix(secs, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxTimestampSkew {
		return fmt.Errorf("timestamp outside %s window", MaxTimestampSkew)
	}
	return nil
}
