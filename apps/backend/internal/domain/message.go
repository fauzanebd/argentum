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

// MessageRepository is the persistence contract for chat messages.
type MessageRepository interface {
	Append(ctx context.Context, m *Message) error
	ListByThread(ctx context.Context, threadID string, limit, offset int) ([]*Message, error)
	CountByThread(ctx context.Context, threadID string) (int, error)
}
