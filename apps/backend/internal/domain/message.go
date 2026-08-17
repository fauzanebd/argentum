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

// NextStep is one thing worth asking next, written by the agent that just
// answered (T-Q10).
//
// It travels on the assistant message's Metadata under `next_steps` rather than
// in a column or a table of its own. Two reasons, and the second is the load
// bearing one. `messages.metadata` already exists and is already marshalled at
// both ends, so this needs no migration. And a suggestion has no life outside
// the answer it belongs to: it is not a queue, it does not expire, and nothing
// ever reads one without the message around it.
//
// **`models.AgentResponse.FollowUpQuestions` is deliberately not this type.**
// That field is the legacy WhatsApp shape, its own comment says new callers
// should use the chat pipeline, and nothing has ever populated it. Writing to it
// as well would make two vocabularies for one idea.
type NextStep struct {
	// Label is the chip's text, ≤ 48 characters. Truncated server-side rather
	// than trusted, because the model treats a length rule as advice.
	Label string `json:"label"`
	// Prompt is what a click puts in the composer. It is never sent
	// automatically — see the frontend rule in T-U13, which is the same rule and
	// the same reason as the starter questions: a turn that runs before the
	// reader has read it teaches nothing and spends a credit.
	Prompt string `json:"prompt"`
	// Recommended marks the one step the agent would take. At most one is true,
	// enforced after the parse: a model asked for "at most one" returns two often
	// enough that the rule has to be applied rather than requested.
	Recommended bool `json:"recommended"`
	// Why is one clause, shown on the recommended step only. It is the difference
	// between a row of buttons and a suggestion.
	Why string `json:"why,omitempty"`
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
