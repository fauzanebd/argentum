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
	ChannelDiscord   Channel = "discord"
	ChannelLark      Channel = "lark"
)

// ConversationThread is one logical conversation. Each phone number gets its
// own thread chain; threads auto-split on long idle gaps + topic shifts.
// For Lark, each Lark reply-thread maps 1:1 to one ConversationThread row
// (one thread = one agent memory); LarkThreadKey is the lookup key
// (thread_id || root_id || message_id, whichever the event surfaces).
type ConversationThread struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	Channel       Channel   `json:"channel"`
	PhoneNumber   string    `json:"phone_number,omitempty"`     // empty for dashboard/discord/lark threads
	UserID        string    `json:"user_id,omitempty"`          // empty for WA/discord/lark threads from non-account holders
	DiscordUserID string    `json:"discord_user_id,omitempty"`  // empty for non-discord threads
	LarkChatID    string    `json:"lark_chat_id,omitempty"`     // empty for non-lark threads
	LarkThreadKey string    `json:"lark_thread_key,omitempty"`  // empty for non-lark threads
	LarkOpenID    string    `json:"lark_open_id,omitempty"`     // initiating user's open_id; empty for non-lark threads
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
	// LatestForDiscordUser returns the most recent non-archived discord thread
	// for a (companyID, discordUserID) pair. ErrNotFound if no thread exists.
	LatestForDiscordUser(ctx context.Context, companyID, discordUserID string) (*ConversationThread, error)
	// LatestForLark returns the non-archived lark thread for a
	// (companyID, larkThreadKey) pair. One Lark reply-thread = one row.
	// ErrNotFound if no thread exists.
	LatestForLark(ctx context.Context, companyID, larkThreadKey string) (*ConversationThread, error)
	ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*ConversationThread, error)
	UpdateSummary(ctx context.Context, id, title, summary string) error
	Touch(ctx context.Context, id string, at time.Time) error
	Archive(ctx context.Context, id string) error
	// Delete removes a thread if it has no messages; otherwise no-ops.
	// The caller should archive first if soft-delete is desired.
	Delete(ctx context.Context, id string) error
}
