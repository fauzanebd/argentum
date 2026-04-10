package models

import (
	"time"

	"github.com/google/uuid"
)

// Message represents an incoming WhatsApp message
type Message struct {
	ID          string    `json:"id"`
	BusinessID  string    `json:"business_id"`
	PhoneNumber string    `json:"phone_number"`
	Body        string    `json:"body"`
	Timestamp   time.Time `json:"timestamp"`
	MessageType string    `json:"message_type"`
}

// NewMessage creates a new message with generated ID
func NewMessage(businessID, phoneNumber, body string) *Message {
	return &Message{
		ID:          uuid.New().String(),
		BusinessID:  businessID,
		PhoneNumber: phoneNumber,
		Body:        body,
		Timestamp:   time.Now(),
		MessageType: "text",
	}
}

// QueryResult represents the result of a SQL query
type QueryResult struct {
	SQL        string                   `json:"sql"`
	Columns    []string                 `json:"columns"`
	Rows       []map[string]interface{} `json:"rows"`
	RowCount   int                      `json:"row_count"`
	Duration   time.Duration            `json:"duration"`
	ExecutedAt time.Time                `json:"executed_at"`
}

// AgentResponse represents the agent's response to a query
type AgentResponse struct {
	MessageID         string       `json:"message_id"`
	Query             string       `json:"query"`
	Insight           string       `json:"insight"`
	QueryResult       *QueryResult `json:"query_result,omitempty"`
	DashboardURL      string       `json:"dashboard_url,omitempty"`
	FollowUpQuestions []string     `json:"follow_up_questions,omitempty"`
	Error             string       `json:"error,omitempty"`
}

// ConversationTurn represents a single turn in a conversation
type ConversationTurn struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Response  string    `json:"response"`
	Timestamp time.Time `json:"timestamp"`
}

// ConversationContext holds the conversation state
type ConversationContext struct {
	SessionID    string             `json:"session_id"`
	PhoneNumber  string             `json:"phone_number"`
	BusinessID   string             `json:"business_id"`
	Turns        []ConversationTurn `json:"turns"`
	Summary      string             `json:"summary"`
	LastActivity time.Time          `json:"last_activity"`
}

// WhatsAppWebhookPayload represents the incoming webhook from WhatsApp
type WhatsAppWebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

// WhatsAppMessageRequest represents an outgoing message to WhatsApp
type WhatsAppMessageRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Text             struct {
		PreviewURL bool   `json:"preview_url"`
		Body       string `json:"body"`
	} `json:"text"`
}

// QueueMessage represents a message sent to the queue
type QueueMessage struct {
	MessageID   string    `json:"message_id"`
	BusinessID  string    `json:"business_id"`
	PhoneNumber string    `json:"phone_number"`
	Body        string    `json:"body"`
	Timestamp   time.Time `json:"timestamp"`
}

// SchemaMetadata represents database schema information
type SchemaMetadata struct {
	Tables []TableMetadata `json:"tables"`
}

// TableMetadata represents metadata for a single table
type TableMetadata struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Columns     []ColumnMetadata `json:"columns"`
}

// ColumnMetadata represents metadata for a single column
type ColumnMetadata struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	IsNullable  bool   `json:"is_nullable"`
	IsPrimary   bool   `json:"is_primary_key"`
}
