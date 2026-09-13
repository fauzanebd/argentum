// Package peermemory keeps an agent in a room from reading its colleagues' turns
// as its own (T-N11).
//
// **The hole.** The SDK's conversation memory is one buffer per company and
// thread — `RedisMemory` keys it `org:conversation`, and ChatRunner sets those to
// the company and the thread — and every provider replays that buffer into every
// request. Until T-N2 a thread had one agent, so the buffer was one agent's. In a
// room it is everybody's, and what the next agent read was:
//
//   - **a colleague's reply, as an `assistant` message** — as though it had said
//     it itself. Unfenced, unlabelled and untainted: T-N5's laundering path, with
//     no nudge anywhere in it;
//   - **a colleague's tool calls and raw results** — query rows from a source this
//     agent's allowlist may not reach. That is a scope leak, not only a trust one;
//   - **a colleague's composed prompt** — its source catalog, its metrics, its
//     prior-work block — as this agent's own user message.
//
// **The fix is a view, not a second store.** Every message is stamped on the way
// in with the agent whose turn wrote it, and each read is rewritten for the agent
// reading: its own turns exactly as written, a colleague's words fenced under the
// colleague's name, a colleague's tool plumbing not at all. Nothing is deleted from
// the buffer, so the room's other agents lose nothing by one agent's reading of it.
//
// **What this deliberately does not do is inherit taint.** A colleague's earlier
// turn may have read a supplier's PDF; T-N5 carries that taint across a *nudge*,
// which is a live hand-off, and not across *history*. Inheriting from history would
// require approval on every room turn after anyone read a document — T-H9's off
// switch — and a single agent reading its own earlier summary of a PDF is not gated
// either. The reading turn records `agent`, which is the fact; the cross-turn gap
// is T-H9's, and is filed there.
package peermemory

import (
	"context"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/taint"
)

// The stamp's keys, on interfaces.Message.Metadata. Prefixed because the SDK
// writes that map too (`tool_name` on a tool result), and a collision would be a
// stamp nobody wrote. RedisMemory JSON-encodes the whole message, so string
// values come back as strings.
const (
	// KeyAgentID is the agent whose turn wrote the message. **Present and empty is
	// a real value**: unattributed — a person's words replayed from Postgres, or a
	// turn that ran unscoped — and every agent reads it as written. Absent means
	// written before this package existed, which is read as written too.
	KeyAgentID = "argentum_agent_id"
	// KeyAgentName is that agent's roster name when the message was written: the
	// fence's label. From the turn's scope, which the worker resolved from the
	// row, never from anything the message says about itself.
	KeyAgentName = "argentum_agent_name"
	// KeyQuestion is the person's words on a user message, beside the composed
	// prompt the SDK stores as its content. It is what a colleague reads instead of
	// that prompt.
	KeyQuestion = "argentum_question"
)

type questionKey struct{}

// WithQuestion records, for this turn, the words a colleague may read of its
// prompt: the person's message, or a peer's message fenced as the turn read it.
// ChatRunner installs it before the agent runs, because the SDK writes the
// composed prompt into memory and the context blocks around those words are this
// agent's, not the room's.
func WithQuestion(ctx context.Context, words string) context.Context {
	return context.WithValue(ctx, questionKey{}, words)
}

// Stamp attributes a message explicitly. [Memory.AddMessage] stamps from the
// turn's scope only when a message carries no stamp, so this is how a caller that
// knows better says so — hydration replays every agent's rows inside one agent's
// turn, and stamping those from the scope would attribute them all to the reader.
func Stamp(msg interfaces.Message, agentID, agentName string) interfaces.Message {
	md := make(map[string]interface{}, len(msg.Metadata)+2)
	for k, v := range msg.Metadata {
		md[k] = v
	}
	md[KeyAgentID] = agentID
	if agentName != "" {
		md[KeyAgentName] = agentName
	}
	msg.Metadata = md
	return msg
}

// Memory is the stamping, rewriting view over the SDK's memory.
//
// It implements interfaces.ConversationMemory whatever it wraps, because
// ChatRunner.hydrateMemory type-asserts that interface to ask whether the buffer
// is already warm — a wrapper that hid it would re-hydrate on every turn and
// duplicate the history. Where the inner memory lacks a method, the answer is the
// SDK's own fallback for a memory without it.
type Memory struct{ inner interfaces.Memory }

// Wrap returns inner behind the view, or nil for a nil memory — never a non-nil
// interface holding a nil pointer, which the SDK's `a.memory != nil` checks would
// read as a memory that exists.
func Wrap(inner interfaces.Memory) interfaces.Memory {
	if inner == nil {
		return nil
	}
	return &Memory{inner: inner}
}

// Unwrap returns the memory underneath.
func (m *Memory) Unwrap() interfaces.Memory { return m.inner }

// AddMessage stamps a message with the turn that wrote it, unless it already
// carries a stamp, and stores it.
func (m *Memory) AddMessage(ctx context.Context, msg interfaces.Message) error {
	if _, stamped := msg.Metadata[KeyAgentID]; !stamped {
		scope := agentscope.FromContext(ctx)
		msg = Stamp(msg, scope.AgentID, scope.Name)
		if msg.Role == interfaces.MessageRoleUser {
			if q, ok := ctx.Value(questionKey{}).(string); ok {
				msg.Metadata[KeyQuestion] = q
			}
		}
	}
	return m.inner.AddMessage(ctx, msg)
}

// GetMessages is the read every provider replays into its request, rewritten for
// the agent reading. A turn with no agent — the eval harness, a company whose
// roster failed to load — reads everything as written, which is what every turn
// did before rooms existed.
func (m *Memory) GetMessages(ctx context.Context, options ...interfaces.GetMessagesOption) ([]interfaces.Message, error) {
	msgs, err := m.inner.GetMessages(ctx, options...)
	if err != nil {
		return msgs, err
	}
	reader := agentscope.AgentID(ctx)
	if reader == "" || len(msgs) == 0 {
		return msgs, nil
	}
	return viewFor(ctx, reader, msgs), nil
}

// viewFor is the rule, one message at a time:
//
//   - the reader's own, a person's, and anything unstamped: as written;
//   - a colleague's **reply**: a user-role message, fenced under the colleague's
//     name, and the turn marked KindAgent. User-role because the fence's rule lives
//     in the user turn (T-N5, decision 5) and because an `assistant` message is
//     what the reader believes it said;
//   - a colleague's **tool call or tool result**: dropped. The result is that
//     agent's data, from that agent's sources; the call's id answers a result the
//     reader never asked for, and replaying one without the other is a request
//     both OpenAI and Anthropic reject. What the colleague concluded from them is
//     in its reply;
//   - a colleague's **prompt**: replaced by the person's words it recorded, and
//     dropped when it recorded none — the composed prompt is that agent's source
//     catalog and context, and there is no safe way to cut the words back out;
//   - a question already standing immediately above: dropped, because a message
//     addressing two agents was written into the buffer once by each.
func viewFor(ctx context.Context, reader string, msgs []interfaces.Message) []interfaces.Message {
	out := make([]interfaces.Message, 0, len(msgs))
	lastQuestion := ""
	for _, msg := range msgs {
		author, stamped := authorOf(msg)
		if !stamped || author == "" || author == reader {
			if msg.Role == interfaces.MessageRoleUser {
				lastQuestion, _ = msg.Metadata[KeyQuestion].(string)
			} else {
				lastQuestion = ""
			}
			out = append(out, msg)
			continue
		}
		switch msg.Role {
		case interfaces.MessageRoleUser:
			q, _ := msg.Metadata[KeyQuestion].(string)
			if strings.TrimSpace(q) == "" || q == lastQuestion {
				continue
			}
			lastQuestion = q
			out = append(out, interfaces.Message{Role: interfaces.MessageRoleUser, Content: q, Metadata: msg.Metadata})
		case interfaces.MessageRoleAssistant:
			// The SDK stores a tool-calling iteration as an assistant message with
			// empty content; the calls go, and so does the message.
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			name, _ := msg.Metadata[KeyAgentName].(string)
			taint.Mark(ctx, taint.KindAgent, name)
			lastQuestion = ""
			out = append(out, interfaces.Message{
				Role:     interfaces.MessageRoleUser,
				Content:  guardrails.FencePeer(name, msg.Content),
				Metadata: msg.Metadata,
			})
		case interfaces.MessageRoleTool:
			continue
		default:
			// A system message is the memory's own — a summary — and nobody's turn.
			lastQuestion = ""
			out = append(out, msg)
		}
	}
	return out
}

func authorOf(msg interfaces.Message) (string, bool) {
	v, ok := msg.Metadata[KeyAgentID]
	if !ok {
		return "", false
	}
	id, _ := v.(string)
	return id, true
}

// Clear clears the conversation, for every agent in it.
func (m *Memory) Clear(ctx context.Context) error { return m.inner.Clear(ctx) }

// GetConversationMessages is the raw buffer, unrewritten. Its one caller is
// hydration asking whether the buffer is warm, which is a count and not a read
// that reaches a model — rewriting it would make an agent's view of an empty
// room's buffer look cold, and re-hydrate on top of it.
func (m *Memory) GetConversationMessages(ctx context.Context, conversationID string) ([]interfaces.Message, error) {
	if c, ok := m.inner.(interfaces.ConversationMemory); ok {
		return c.GetConversationMessages(ctx, conversationID)
	}
	return []interfaces.Message{}, nil
}

// GetAllConversations forwards to the inner memory.
func (m *Memory) GetAllConversations(ctx context.Context) ([]string, error) {
	if c, ok := m.inner.(interfaces.ConversationMemory); ok {
		return c.GetAllConversations(ctx)
	}
	return []string{}, nil
}

// GetMemoryStatistics forwards to the inner memory.
func (m *Memory) GetMemoryStatistics(ctx context.Context) (int, int, error) {
	if c, ok := m.inner.(interfaces.ConversationMemory); ok {
		return c.GetMemoryStatistics(ctx)
	}
	return 0, 0, nil
}
