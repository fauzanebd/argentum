package domain

import (
	"context"
	"time"
)

// MessageRole is the role of the sender in a conversation turn.
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
	MessageRoleSystem    MessageRole = "system"
)

// Message is a single turn (user, assistant, or tool) inside a thread.
type Message struct {
	ID        string                 `json:"id"`
	ThreadID  string                 `json:"thread_id"`
	Role      MessageRole            `json:"role"`
	Content   string                 `json:"content"`
	ToolCalls map[string]interface{} `json:"tool_calls,omitempty"`
	TokensIn  int                    `json:"tokens_in,omitempty"`
	TokensOut int                    `json:"tokens_out,omitempty"`
	LatencyMs int64                  `json:"latency_ms,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// MessageFilter is the keyset page a `/v1` transcript read asks for (T-A3).
//
// There is no channel or role filter: a transcript with the tool turns removed
// is a transcript that does not explain itself, and a caller that wants only
// the assistant's words can read `role`.
type MessageFilter struct {
	CursorTime time.Time
	CursorID   string
	Limit      int
}

// MessageRepository is the persistence contract for chat messages.
type MessageRepository interface {
	Append(ctx context.Context, m *Message) error
	ListByThread(ctx context.Context, threadID string, limit, offset int) ([]*Message, error)
	// ListPageByThread walks a transcript oldest-first, unlike every other
	// keyset listing in this codebase. A conversation read newest-first is a
	// conversation nobody can follow; the pagination cost is identical either
	// way, and only the direction of the predicate changes.
	ListPageByThread(ctx context.Context, threadID string, f MessageFilter) ([]*Message, bool, error)
	// LatestByThread returns the newest message of any role, or ErrNotFound for
	// an empty thread.
	//
	// It answers "is this thread settled or is a turn running?" — an assistant
	// message last means settled, a user message last means the agent is still
	// working on it. That question cannot be answered from the thread row:
	// `last_message_at` is written by the API's clock and `created_at` by
	// Postgres's, so the two disagree by microseconds and any comparison
	// between them is a coin toss. Comparing two rows written by the same
	// clock is not.
	LatestByThread(ctx context.Context, threadID string) (*Message, error)
	// LatestAssistantSince finds the answer one turn produced, or ErrNotFound
	// when the turn has not answered yet.
	//
	// `since` is what makes it a turn rather than a thread: without it a caller
	// re-attaching to a stream would be handed the *previous* answer and told
	// it was this one. Same shape, and the same reason, as
	// DocumentRepository.NewestForThreadSince.
	LatestAssistantSince(ctx context.Context, threadID string, since time.Time) (*Message, error)
	CountByThread(ctx context.Context, threadID string) (int, error)
}
