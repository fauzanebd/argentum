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
	ChannelSlack     Channel = "slack"
	// ChannelAPI is a turn started over the public `/v1` API (T-A1). It is
	// the only channel with no outbound provider: the reply is the HTTP
	// response the caller is already holding open, so ChatRunner.completeWith
	// deliberately does nothing for it.
	ChannelAPI Channel = "api"
)

// ConversationThread is one logical conversation. Each phone number gets its
// own thread chain; threads auto-split on long idle gaps + topic shifts.
// For Lark, each Lark reply-thread maps 1:1 to one ConversationThread row
// (one thread = one agent memory); LarkThreadKey is the lookup key
// (thread_id || root_id || message_id, whichever the event surfaces).
type ConversationThread struct {
	ID            string  `json:"id"`
	CompanyID     string  `json:"company_id"`
	Channel       Channel `json:"channel"`
	PhoneNumber   string  `json:"phone_number,omitempty"`    // empty for dashboard/discord/lark threads
	UserID        string  `json:"user_id,omitempty"`         // empty for WA/discord/lark threads from non-account holders
	DiscordUserID string  `json:"discord_user_id,omitempty"` // empty for non-discord threads
	LarkChatID    string  `json:"lark_chat_id,omitempty"`    // empty for non-lark threads
	LarkThreadKey string  `json:"lark_thread_key,omitempty"` // empty for non-lark threads
	LarkOpenID    string  `json:"lark_open_id,omitempty"`    // initiating user's open_id; empty for non-lark threads
	// Slack keys on two things at once, and 049's comment says why: a message
	// inside a thread is found by (SlackChannelID, SlackThreadTS), a top-level
	// mention or DM by (SlackChannelID, SlackUserID). A thread opened by the
	// latter stores the ts its reply will hang under, so the two agree.
	SlackTeamID    string `json:"slack_team_id,omitempty"`    // empty for non-slack threads
	SlackChannelID string `json:"slack_channel_id,omitempty"` // empty for non-slack threads
	SlackThreadTS  string `json:"slack_thread_ts,omitempty"`  // empty for non-slack threads
	SlackUserID    string `json:"slack_user_id,omitempty"`    // initiating user's id; empty for non-slack threads
	// APIUserRef is the tenant's own identifier for whoever the API call was
	// made on behalf of (T-A1). It is opaque to us by design: an API key
	// belongs to a company, so the only identity available is the one the
	// caller supplies. Empty for non-api threads.
	APIUserRef string `json:"api_user_ref,omitempty"`
	// AgentID is the roster agent this conversation runs as (T-S2). Empty
	// means the company default, which is what every thread predating the
	// roster resolves to and what a thread whose agent was deleted falls back
	// to — the column is ON DELETE SET NULL precisely so that a tidied roster
	// cannot strand a conversation.
	AgentID       string    `json:"agent_id,omitempty"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary,omitempty"`
	LastMessageAt time.Time `json:"last_message_at"`
	IsArchived    bool      `json:"is_archived"`
	CreatedAt     time.Time `json:"created_at"`
}

// ThreadFilter narrows a keyset-paginated thread listing (T-A3).
//
// Channel is a filter rather than an assumption because the table holds every
// channel's conversations in one place. `/v1` always sets it to ChannelAPI: a
// machine credential listing the threads of named people who have dashboard
// sessions is a leaked key reading the staff's chat history, and the tenant's
// own audit surface for those is the dashboard, which is role-gated.
//
// APIUserRef narrows further, to one end user of the tenant's own product. It
// is what makes "neither user_ref can read the other's thread" enforceable by
// us rather than by the integrator remembering to filter.
type ThreadFilter struct {
	Channel    Channel
	APIUserRef string
	CursorTime time.Time
	CursorID   string
	Limit      int
}

// ThreadRepository is the persistence contract for conversation threads.
type ThreadRepository interface {
	Create(ctx context.Context, t *ConversationThread) error
	GetByID(ctx context.Context, id string) (*ConversationThread, error)
	// GetForCompany is GetByID with the tenant boundary inside the query.
	// `/v1` uses only this one, for the reason DocumentRepository states: a
	// handler that fetches first and compares afterwards is one forgotten
	// comparison away from a cross-tenant read.
	GetForCompany(ctx context.Context, companyID, id string) (*ConversationThread, error)
	// ListPage returns one keyset page newest-first plus whether another page
	// exists. Distinct from ListByCompany, which is the dashboard's offset
	// listing: an offset walk over a table that gains rows while it is being
	// paged shows an item twice or misses one entirely.
	ListPage(ctx context.Context, companyID string, f ThreadFilter) ([]*ConversationThread, bool, error)
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
	// LatestForSlackThread returns the non-archived slack thread for a
	// (companyID, slackChannelID, slackThreadTS) triple. One Slack thread =
	// one row. ErrNotFound if no thread exists.
	LatestForSlackThread(ctx context.Context, companyID, slackChannelID, slackThreadTS string) (*ConversationThread, error)
	// LatestForSlackUser returns the most recent non-archived slack thread for
	// a (companyID, slackChannelID, slackUserID) triple. It answers the
	// top-level message — one that carries no thread_ts, so there is no thread
	// id to look up yet. ErrNotFound if no thread exists.
	LatestForSlackUser(ctx context.Context, companyID, slackChannelID, slackUserID string) (*ConversationThread, error)
	// LatestForAPIUser returns the most recent non-archived api thread for a
	// (companyID, apiUserRef) pair. ErrNotFound if no thread exists.
	LatestForAPIUser(ctx context.Context, companyID, apiUserRef string) (*ConversationThread, error)
	ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*ConversationThread, error)
	UpdateSummary(ctx context.Context, id, title, summary string) error
	Touch(ctx context.Context, id string, at time.Time) error
	Archive(ctx context.Context, id string) error
	// Delete removes a thread if it has no messages; otherwise no-ops.
	// The caller should archive first if soft-delete is desired.
	Delete(ctx context.Context, id string) error
}
