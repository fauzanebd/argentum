package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
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
	ID       string      `json:"id"`
	ThreadID string      `json:"thread_id"`
	Role     MessageRole `json:"role"`
	Content  string      `json:"content"`
	// AgentID is the roster agent that wrote this message (T-N1), or "" for a
	// user message and for every assistant message written before the column
	// existed on a thread that named no agent.
	//
	// It is the id the turn **actually ran as** — agentscope.AgentID(ctx),
	// which is the same value the audit row and the usage event record — and
	// not the id the queue payload asked for. The two differ whenever an agent
	// was deleted between enqueue and run, and three rows describing one turn
	// must not be able to disagree about who ran it.
	AgentID string `json:"agent_id,omitempty"`
	// AgentName rides along on reads so a transcript does not have to join the
	// roster in the browser. **It is not persisted** — the same arrangement, and
	// the same reason, as AgentChannelBinding.AgentName. A renamed agent
	// therefore renames its past messages, which is the behaviour a reader
	// expects from a roster they can edit.
	AgentName string `json:"agent_name,omitempty"`
	// ToolCalls is every tool the turn ran, in the order it ran them (RIZ-95).
	// Only an assistant answer that streamed has any. Answers written before
	// RIZ-95 have none: nothing saved them.
	ToolCalls ToolCallList           `json:"tool_calls,omitempty"`
	TokensIn  int                    `json:"tokens_in,omitempty"`
	TokensOut int                    `json:"tokens_out,omitempty"`
	LatencyMs int64                  `json:"latency_ms,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// ToolCallRecord is one tool call a turn made, as the answer keeps it (RIZ-95):
// what the dashboard's tool chip needs to say what ran, and nothing more.
//
// Arguments are redacted the way the audit log's are, and Result is cut down
// to the handful of keys the chip reads — a query's rows are not something a
// transcript should carry a second copy of. ChatRunner builds these
// (toolCallRecord); nothing else writes them.
type ToolCallRecord struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Result    map[string]interface{} `json:"result,omitempty"`
}

// ToolCallList is a turn's tool calls in run order — a list, because one tool
// runs many times in a turn and the order is the story.
//
// **It reads two shapes.** `messages.tool_calls` was declared a map keyed by
// tool name long before anything wrote it, and a row in that shape keeps one
// call per tool and no order. UnmarshalJSON turns one into a list, one record
// per key in sorted key order — the order those chips always rendered in,
// because Go's encoder sorts a map's keys when it serves one — so a reader only
// ever sees the list. Anything else (`null`, a string, a number) reads as no
// calls rather than an error: this is display data, and a row nobody can
// render is better shown without its tools than not shown.
type ToolCallList []ToolCallRecord

// UnmarshalJSON accepts the current list shape, the legacy by-name object, and
// anything else as empty. It never returns an error; see the type's comment.
func (l *ToolCallList) UnmarshalJSON(b []byte) error {
	*l = nil
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case '[':
		var records []ToolCallRecord
		if err := json.Unmarshal(b, &records); err == nil && len(records) > 0 {
			*l = records
		}
	case '{':
		var legacy map[string]interface{}
		if err := json.Unmarshal(b, &legacy); err != nil || len(legacy) == 0 {
			return nil
		}
		names := make([]string, 0, len(legacy))
		for name := range legacy {
			names = append(names, name)
		}
		sort.Strings(names)
		records := make([]ToolCallRecord, 0, len(names))
		for _, name := range names {
			rec := ToolCallRecord{Name: name}
			// The legacy value was whatever payload the chip was given, so it
			// lands where the chip will read it from.
			if payload, ok := legacy[name].(map[string]interface{}); ok && len(payload) > 0 {
				rec.Result = payload
			}
			records = append(records, rec)
		}
		*l = records
	}
	return nil
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
	//
	// A room's own lines (`metadata.room_event`, T-N6) are never an answer:
	// "→ Finance: …" is written as the asking agent while its turn is still
	// running, and a settle is a colleague saying it has nothing. scope says
	// whether a colleague's answer counts (T-N10).
	LatestAssistantSince(ctx context.Context, threadID string, since time.Time, scope AnswerScope) (*Message, error)
	CountByThread(ctx context.Context, threadID string) (int, error)
}

// AnswerScope is which assistant rows LatestAssistantSince may return (T-N10).
//
// **Not an agent id, which was tried first.** In a room a colleague asked
// mid-turn answers in the same thread, often first, so "this turn's answer"
// needs a bound. The agent a turn was sent to is the wrong one: a turn whose
// agent was deleted before it ran runs as the company default, and an agent can
// be asked back by the colleague it asked. What separates a caller's turn from a
// colleague's is whether another agent asked for it, which the row records under
// `asked_by`.
type AnswerScope int

const (
	// AnyAnswer is the newest assistant row that is not a room's own line — what
	// attaching to a thread delivers.
	AnyAnswer AnswerScope = iota
	// OwnAnswer also leaves out a colleague's answer: the answer to the question
	// a caller sent.
	OwnAnswer
)
