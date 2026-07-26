package discord

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
)

// Discord signs every interactions request with Ed25519. Verifying is
// required by the API spec — Discord rejects any application whose endpoint
// returns 200 to an unsigned PING. Stateless helpers; the HTTP handler binds
// them to gin in the api router.

// PublicKeyResolver returns the Ed25519 public key (hex) for a given
// application id. Used by the interactions handler to pick the right key per
// tenant (each tenant has their own Discord application).
type PublicKeyResolver interface {
	ResolvePublicKey(ctx context.Context, applicationID string) (string, error)
}

// VerifySignature returns nil iff (timestamp || body) was signed with the
// secret matching the supplied public key (hex). signature is also hex.
func VerifySignature(publicKeyHex, signatureHex, timestamp string, body []byte) error {
	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	sig, err := hex.DecodeString(signatureHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("invalid signature")
	}
	msg := append([]byte(timestamp), body...)
	if !ed25519.Verify(pub, msg, sig) {
		return errors.New("signature mismatch")
	}
	return nil
}

// Interaction type constants from the Discord docs. We only handle PING for
// now; everything else gets a generic ack.
const (
	InteractionTypePing               = 1
	InteractionTypeApplicationCommand = 2
	InteractionTypeMessageComponent   = 3
	InteractionTypeAutocomplete       = 4
	InteractionTypeModalSubmit        = 5
)

// Response type constants.
const (
	InteractionResponsePong                    = 1
	InteractionResponseChannelMessageWithSource = 4
)
