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
	VerifyToken(token, challenge string) bool
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
	switch cfg.Provider {
	case "twilio":
		logrus.Info("Using Twilio WhatsApp provider")
		return NewTwilioClient(cfg.TwilioAccountSID, cfg.TwilioAuthToken, cfg.TwilioFromNumber), nil

	case "whatsapp_business", "":
		logrus.Info("Using WhatsApp Business API provider")
		return NewWhatsAppClient(cfg.APIVersion, cfg.PhoneNumberID, cfg.AccessToken, cfg.AppSecret), nil

	default:
		return nil, fmt.Errorf("unknown WhatsApp provider: %s", cfg.Provider)
	}
}
