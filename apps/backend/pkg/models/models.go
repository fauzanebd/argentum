// Package models holds shared transport-level message types used by the
// WhatsApp providers and webhook handlers. Domain-level entities live in
// internal/domain.
package models

import "time"

// Message is an inbound WhatsApp / Twilio message normalized across providers.
type Message struct {
	ID          string    `json:"id"`
	BusinessID  string    `json:"business_id,omitempty"`
	PhoneNumber string    `json:"phone_number"`
	Body        string    `json:"body"`
	Timestamp   time.Time `json:"timestamp"`
	MessageType string    `json:"message_type"`
}

// AgentResponse is the legacy reply shape used by whatsapp.Provider.SendResponse.
// New callers should prefer publishing a domain.Message via the chat pipeline
// and let the WA sink format outbound text.
type AgentResponse struct {
	MessageID         string   `json:"message_id"`
	Query             string   `json:"query"`
	Insight           string   `json:"insight"`
	DashboardURL      string   `json:"dashboard_url,omitempty"`
	FollowUpQuestions []string `json:"follow_up_questions,omitempty"`
	Error             string   `json:"error,omitempty"`
}

// WhatsAppWebhookPayload models the WhatsApp Business API webhook envelope.
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

// WhatsAppMessageRequest is the outbound shape posted to the WA Business API.
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
