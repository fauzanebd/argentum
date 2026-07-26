package whatsapp

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/sirupsen/logrus"
)

// WhatsAppClient handles WhatsApp Business API interactions
type WhatsAppClient struct {
	apiVersion    string
	phoneNumberID string
	accessToken   string
	appSecret     string
	baseURL       string
	httpClient    *http.Client
}

// NewWhatsAppClient creates a new WhatsApp Business API client
func NewWhatsAppClient(apiVersion, phoneNumberID, accessToken, appSecret string) *WhatsAppClient {
	return &WhatsAppClient{
		apiVersion:    apiVersion,
		phoneNumberID: phoneNumberID,
		accessToken:   accessToken,
		appSecret:     appSecret,
		baseURL:       fmt.Sprintf("https://graph.facebook.com/%s", apiVersion),
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// VerifyWebhook verifies the webhook signature from WhatsApp
// The url parameter is not used for WhatsApp Business API but included for interface compatibility
func (c *WhatsAppClient) VerifyWebhook(body []byte, signature string, url string) bool {
	if c.appSecret == "" {
		logrus.Warn("App secret not configured, skipping signature verification")
		return true
	}

	// The signature is in format "sha256=<hash>"
	if len(signature) < 8 {
		return false
	}
	expectedSignature := signature[7:] // Remove "sha256=" prefix

	mac := hmac.New(sha256.New, []byte(c.appSecret))
	mac.Write(body)
	computedSignature := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSignature), []byte(computedSignature))
}

// VerifyToken validates the webhook verification token
func (c *WhatsAppClient) VerifyToken(token, challenge string) bool {
	return token == challenge
}

// ParseWebhook parses the incoming webhook payload
func (c *WhatsAppClient) ParseWebhook(body []byte) (*models.Message, error) {
	var payload models.WhatsAppWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	// Extract message from payload
	if len(payload.Entry) == 0 || len(payload.Entry[0].Changes) == 0 {
		return nil, fmt.Errorf("no entries or changes in webhook payload")
	}

	change := payload.Entry[0].Changes[0]
	if len(change.Value.Messages) == 0 {
		return nil, fmt.Errorf("no messages in webhook payload")
	}

	msg := change.Value.Messages[0]

	return &models.Message{
		ID:          msg.ID,
		BusinessID:  payload.Entry[0].ID,
		PhoneNumber: msg.From,
		Body:        msg.Text.Body,
		Timestamp:   time.Now(),
		MessageType: msg.Type,
	}, nil
}

// SendMessage sends a text message to a WhatsApp user
func (c *WhatsAppClient) SendMessage(phoneNumber, message string) error {
	url := fmt.Sprintf("%s/%s/messages", c.baseURL, c.phoneNumberID)

	reqBody := models.WhatsAppMessageRequest{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               phoneNumber,
		Type:             "text",
	}
	reqBody.Text.PreviewURL = false
	reqBody.Text.Body = message

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp API error (status %d): %s", resp.StatusCode, string(body))
	}

	logrus.Infof("Message sent successfully to %s", phoneNumber)
	return nil
}

// SendResponse sends the agent response to the user
func (c *WhatsAppClient) SendResponse(phoneNumber string, response *models.AgentResponse) error {
	// Format the message
	message := formatResponse(response)
	return c.SendMessage(phoneNumber, message)
}

// formatResponse formats the agent response for WhatsApp
func formatResponse(response *models.AgentResponse) string {
	var result string

	// Add the insight
	if response.Insight != "" {
		result = response.Insight
	}

	// Add dashboard URL if available
	if response.DashboardURL != "" {
		result += fmt.Sprintf("\n\n📊 View Dashboard: %s", response.DashboardURL)
	}

	// Add follow-up suggestions
	if len(response.FollowUpQuestions) > 0 {
		result += "\n\n💡 You can also ask:"
		for _, q := range response.FollowUpQuestions {
			result += fmt.Sprintf("\n• %s", q)
		}
	}

	// Truncate if too long (WhatsApp limit is ~4096 characters)
	if len(result) > 4000 {
		result = result[:3997] + "..."
	}

	return result
}
