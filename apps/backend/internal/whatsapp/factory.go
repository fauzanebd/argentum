package whatsapp

import (
	"fmt"

	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/sirupsen/logrus"
)

// Provider interface for WhatsApp implementations
type Provider interface {
	SendMessage(phoneNumber, message string) error
	SendResponse(phoneNumber string, response *models.AgentResponse) error
	ParseWebhook(body []byte) (*models.Message, error)
	VerifyWebhook(body []byte, signature string, url string) bool
	// VerifyToken compares the token a caller presented against the one this
	// deployment holds. Only Meta's GET subscription handshake uses it.
	VerifyToken(token, expected string) bool
}

// Transport names which inbound webhook shape a deployment speaks.
//
// The handler needs this as a separate value because the two providers
// authenticate differently and the *request* must not get to choose between
// them: `/webhook/whatsapp` picked the Twilio branch whenever the caller sent
// an X-Twilio-Signature header or a form content type (T-H1), which let anyone
// route a Meta deployment into a verifier that was never implemented.
type Transport string

const (
	// TransportMeta is the WhatsApp Business API: a JSON body signed with
	// HMAC-SHA256 in X-Hub-Signature-256.
	TransportMeta Transport = "whatsapp_business"
	// TransportTwilio is Twilio's programmable messaging: a form body signed
	// with HMAC-SHA1 over URL + sorted parameters in X-Twilio-Signature.
	TransportTwilio Transport = "twilio"
)

// ResolveTransport maps WHATSAPP_PROVIDER onto the transport it selects.
//
// It is the switch NewProvider used to make inline, lifted out so the handler
// and the client cannot end up disagreeing about which provider is running —
// which is the disagreement T-H1 was.
func ResolveTransport(provider string) (Transport, error) {
	switch provider {
	case "twilio":
		return TransportTwilio, nil
	case "whatsapp_business", "":
		return TransportMeta, nil
	default:
		return "", fmt.Errorf("unknown WhatsApp provider: %s", provider)
	}
}

// Config holds WhatsApp provider configuration
type Config struct {
	// Provider selection
	Provider string // "whatsapp_business" or "twilio"

	// WhatsApp Business API credentials
	APIVersion         string
	PhoneNumberID      string
	AccessToken        string
	AppSecret          string
	WebhookVerifyToken string

	// Twilio credentials
	TwilioAccountSID string
	TwilioAuthToken  string
	TwilioFromNumber string
}

// NewProvider creates the appropriate WhatsApp provider based on configuration
func NewProvider(cfg Config) (Provider, error) {
	transport, err := ResolveTransport(cfg.Provider)
	if err != nil {
		return nil, err
	}
	switch transport {
	case TransportTwilio:
		logrus.Info("Using Twilio WhatsApp provider")
		return NewTwilioClient(cfg.TwilioAccountSID, cfg.TwilioAuthToken, cfg.TwilioFromNumber), nil

	default:
		logrus.Info("Using WhatsApp Business API provider")
		return NewWhatsAppClient(cfg.APIVersion, cfg.PhoneNumberID, cfg.AccessToken, cfg.AppSecret), nil
	}
}
