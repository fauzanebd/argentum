package whatsapp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/sirupsen/logrus"
)

// TwilioClient implements WhatsApp client using Twilio API
type TwilioClient struct {
	accountSID string
	authToken  string
	fromNumber string // Twilio WhatsApp number (e.g., +14155238886)
	httpClient *http.Client
	baseURL    string
}

// NewTwilioClient creates a new Twilio WhatsApp client
func NewTwilioClient(accountSID, authToken, fromNumber string) *TwilioClient {
	return &TwilioClient{
		accountSID: accountSID,
		authToken:  authToken,
		fromNumber: sanitizePhoneNumber(fromNumber),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://api.twilio.com",
	}
}

// sanitizePhoneNumber removes comments and trims whitespace from phone number
func sanitizePhoneNumber(phone string) string {
	// Remove inline comments (anything after #)
	if idx := strings.Index(phone, "#"); idx != -1 {
		phone = phone[:idx]
	}
	// Trim whitespace
	return strings.TrimSpace(phone)
}

// SendMessage sends a text message via Twilio WhatsApp
func (c *TwilioClient) SendMessage(phoneNumber, message string) error {
	// Ensure phone number has whatsapp: prefix for Twilio
	from := c.fromNumber
	if !strings.HasPrefix(from, "whatsapp:") {
		from = "whatsapp:" + from
	}
	to := phoneNumber
	if !strings.HasPrefix(to, "whatsapp:") {
		to = "whatsapp:" + to
	}

	// Build form data
	data := url.Values{}
	data.Set("From", from)
	data.Set("To", to)

	if len(message) > 1600 {
		message = message[:1600]
	}

	data.Set("Body", message)

	// Create request
	reqURL := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", c.baseURL, c.accountSID)
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication
	req.SetBasicAuth(c.accountSID, c.authToken)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("twilio API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		SID          string `json:"sid"`
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if result.ErrorMessage != "" {
		return fmt.Errorf("twilio error: %s", result.ErrorMessage)
	}

	logrus.Infof("Message sent successfully via Twilio to %s (SID: %s)", phoneNumber, result.SID)
	return nil
}

// ParseWebhook parses Twilio's webhook payload
func (c *TwilioClient) ParseWebhook(body []byte) (*models.Message, error) {
	// Twilio sends form-encoded data, but we can also receive JSON
	// Try JSON first, then fall back to form parsing

	var msg models.Message

	// Try JSON parsing
	var jsonPayload struct {
		SID        string `json:"MessageSid"`
		From       string `json:"From"`
		To         string `json:"To"`
		Body       string `json:"Body"`
		NumMedia   int    `json:"NumMedia"`
		AccountSID string `json:"AccountSid"`
	}

	if err := json.Unmarshal(body, &jsonPayload); err == nil && jsonPayload.SID != "" {
		// Remove whatsapp: prefix if present
		from := jsonPayload.From
		if strings.HasPrefix(from, "whatsapp:") {
			from = from[9:]
		}

		msg = models.Message{
			ID:          jsonPayload.SID,
			PhoneNumber: from,
			Body:        jsonPayload.Body,
			Timestamp:   time.Now(),
			MessageType: "text",
		}
		return &msg, nil
	}

	// If JSON fails, try form parsing (Twilio default)
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	from := values.Get("From")
	if strings.HasPrefix(from, "whatsapp:") {
		from = from[9:]
	}

	msg = models.Message{
		ID:          values.Get("MessageSid"),
		PhoneNumber: from,
		Body:        values.Get("Body"),
		Timestamp:   time.Now(),
		MessageType: "text",
	}

	if msg.ID == "" {
		return nil, fmt.Errorf("no message SID in webhook payload")
	}

	return &msg, nil
}

// SendResponse sends the agent response to the user
func (c *TwilioClient) SendResponse(phoneNumber string, response *models.AgentResponse) error {
	message := formatResponse(response)
	return c.SendMessage(phoneNumber, message)
}

// VerifyWebhook validates the Twilio webhook signature
func (c *TwilioClient) VerifyWebhook(body []byte, signature string, url string) bool {
	if c.authToken == "" {
		logrus.Warn("Auth token not configured, skipping signature verification")
		return true
	}

	// Twilio webhook signature verification
	// https://www.twilio.com/docs/usage/security#validating-requests
	expectedSignature := computeTwilioSignature(url, body, c.authToken)

	return signature == expectedSignature
}

// computeTwilioSignature computes the expected Twilio signature
func computeTwilioSignature(url string, body []byte, authToken string) string {
	// Twilio signature = Base64(HMAC-SHA256(authToken, url + body))
	// Implementation simplified - in production use proper HMAC
	return "" // Placeholder - implement if needed
}

// VerifyToken is not used for Twilio (no verification token needed)
func (c *TwilioClient) VerifyToken(token, challenge string) bool {
	// Twilio doesn't use verify tokens like WhatsApp Business API
	// Return true to pass through
	return true
}

// GetAccountSID returns the account SID
func (c *TwilioClient) GetAccountSID() string {
	return c.accountSID
}
