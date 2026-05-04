package domain

import (
	"context"
	"time"
)

// Channel is the entry point a conversation came in through.
type Channel string

const (
	ChannelWhatsApp  Channel = "whatsapp"
	ChannelDashboard Channel = "dashboard"
)

// ConversationThread is one logical conversation. Each phone number gets its
// own thread chain; threads auto-split on long idle gaps + topic shifts.
type ConversationThread struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	Channel       Channel   `json:"channel"`
	PhoneNumber   string    `json:"phone_number,omitempty"` // empty for dashboard threads
	UserID        string    `json:"user_id,omitempty"`      // empty for WA threads from non-account holders
	Title         string    `json:"title"`
	Summary       string    `json:"summary,omitempty"`
	LastMessageAt time.Time `json:"last_message_at"`
	IsArchived    bool      `json:"is_archived"`
	CreatedAt     time.Time `json:"created_at"`
}

// ThreadRepository is the persistence contract for conversation threads.
type ThreadRepository interface {
	Create(ctx context.Context, t *ConversationThread) error
	GetByID(ctx context.Context, id string) (*ConversationThread, error)
	// LatestForPhone returns the most recent non-archived thread for a phone
	// number within a company. ErrNotFound if no thread exists yet.
	LatestForPhone(ctx context.Context, companyID, phoneNumber string) (*ConversationThread, error)
	// LatestForUser returns the most recent non-archived dashboard thread for
	// a user.
	LatestForUser(ctx context.Context, companyID, userID string) (*ConversationThread, error)
	ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*ConversationThread, error)
	UpdateSummary(ctx context.Context, id, title, summary string) error
	Touch(ctx context.Context, id string, at time.Time) error
	Archive(ctx context.Context, id string) error
	// Delete removes a thread if it has no messages; otherwise no-ops.
	// The caller should archive first if soft-delete is desired.
	Delete(ctx context.Context, id string) error
}
