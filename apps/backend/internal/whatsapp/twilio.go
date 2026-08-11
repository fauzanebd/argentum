package whatsapp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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
		from := strings.TrimPrefix(jsonPayload.From, "whatsapp:")

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

	from := strings.TrimPrefix(values.Get("From"), "whatsapp:")

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

// VerifyWebhook validates the X-Twilio-Signature header on an inbound webhook.
//
// Twilio signs HMAC-SHA1 — not SHA256, which the comment this replaced claimed —
// over the request URL followed by the POST parameters sorted by name, each name
// written immediately before its value with no delimiter, keyed by the account's
// auth token and base64-encoded.
// https://www.twilio.com/docs/usage/security#validating-requests
//
// body is the form the caller already parsed and re-encoded with
// url.Values.Encode. That is a lossless round trip back to the parameters
// Twilio signed, and it keeps the Provider interface one shape for both
// transports rather than adding a Twilio-only method to it.
func (c *TwilioClient) VerifyWebhook(body []byte, signature string, requestURL string) bool {
	if c.authToken == "" {
		// Fail closed. This returned true until T-H1, which made the signature
		// check a no-op on any deployment that had not set TWILIO_AUTH_TOKEN —
		// and the signature is the only authentication /webhook/whatsapp has.
		// Config validation refuses to boot a production process without the
		// token; this is the request-time half of the same rule, for the
		// development box that is allowed to boot without one.
		logrus.Warn("Twilio auth token is not configured; the webhook signature cannot be verified")
		return false
	}
	if signature == "" {
		return false
	}
	params, err := url.ParseQuery(string(body))
	if err != nil {
		return false
	}
	got, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(got, twilioSignature(c.authToken, requestURL, params))
}

// twilioSignature computes the raw HMAC Twilio would have sent for this URL and
// these parameters.
//
// Repeated parameter names are written in arrival order under one sorted key.
// Twilio's own helper libraries model the form as a map and therefore have no
// answer for the case at all, and a WhatsApp callback does not repeat a name —
// so this picks the one order that is at least deterministic rather than
// pretending there is a spec to follow.
func twilioSignature(authToken, requestURL string, params url.Values) []byte {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(requestURL))
	for _, k := range keys {
		for _, v := range params[k] {
			mac.Write([]byte(k))
			mac.Write([]byte(v))
		}
	}
	return mac.Sum(nil)
}

// VerifyToken refuses the Meta GET handshake, which Twilio has no equivalent of.
//
// It returned true unconditionally, so a Twilio deployment would echo any
// `hub.challenge` back to anyone who asked. The handler no longer routes the
// handshake here at all (it is chosen by transport now), and this is the second
// answer to the same question.
func (c *TwilioClient) VerifyToken(token, expected string) bool {
	return false
}

// GetAccountSID returns the account SID
func (c *TwilioClient) GetAccountSID() string {
	return c.accountSID
}
