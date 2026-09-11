package app

import (
	"context"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-N1: which agent wrote this message.
//
// Three rows describe one turn — the message, the audit row and the usage
// event — and before this ticket only two of them named the agent. The tests
// below pin the property that makes them agree: all three read
// agentscope.FromContext, so none of them can be handed a different answer by a
// call site that forgot to pass the same thing.

// capturingMessages records what Append was asked to persist. stubMessages
// discards it, which is enough for every test that only cares that a message
// was written and not enough for one that cares what was in it.
type capturingMessages struct {
	stubMessages
	appended []domain.Message
}

func (c *capturingMessages) Append(_ context.Context, m *domain.Message) error {
	m.ID = "msg-1"
	c.appended = append(c.appended, *m)
	return nil
}

// capturingBus keeps every published event so a test can assert on the stamp
// rather than on the side effect.
type capturingBus struct{ events []ChatEvent }

func (b *capturingBus) Publish(_ string, evt ChatEvent) error {
	b.events = append(b.events, evt)
	return nil
}
func (b *capturingBus) PublishOutbound(OutboundEvent) error { return nil }

func attributionRunner(t *testing.T) (*ChatRunner, *capturingMessages, *capturingBus) {
	t.Helper()
	msgs := &capturingMessages{}
	bus := &capturingBus{}
	threads := quietThreadRepo{&fakeThreadRepo{latestErr: domain.ErrNotFound}}
	svc := NewThreadService(threads, msgs, nil, nil, ThreadServiceConfig{
		IdleMinutes: 30, SummaryEveryNTurns: 8,
	})
	r := NewChatRunner(svc, msgs, threads, nil, nil, nil, bus, nil, nil, nil, 20)
	return r, msgs, bus
}

// scoped is the context a turn carries once ChatRunner has resolved its agent.
func scoped(id, name string) context.Context {
	return agentscope.WithScope(context.Background(), agentscope.Scope{AgentID: id, Name: name})
}

func TestAnAssistantMessageRecordsTheAgentThatWroteIt(t *testing.T) {
	r, msgs, _ := attributionRunner(t)

	r.completeWith(scoped("ag-fin", "Finance"), queue.ChatRunPayload{
		ThreadID: "th-1", UserMsgID: "um-1", Channel: domain.ChannelDashboard,
	}, "margin fell 4%", 0, 0, 0, nil, "")

	if len(msgs.appended) != 1 {
		t.Fatalf("appended %d messages, want 1", len(msgs.appended))
	}
	if got := msgs.appended[0].AgentID; got != "ag-fin" {
		t.Errorf("message agent_id = %q, want ag-fin", got)
	}
}

// The load-bearing one. resolveAgent falls back to the company default when the
// payload names an agent that has been deleted since the turn was queued
// (chat_runner.go, T-S2). The message row must record the agent the turn
// **ran as**, not the one the payload asked for — otherwise the transcript and
// the audit log disagree about the same turn, and the audit log is the one
// that is right.
func TestTheMessageRecordsTheAgentTheTurnRanAsNotTheOneThePayloadNamed(t *testing.T) {
	r, msgs, _ := attributionRunner(t)
	roster := &fakeRoster{
		byID: map[string]*domain.Agent{"ag-def": agentRow("ag-def", "Analyst")},
		def:  agentRow("ag-def", "Analyst"),
	}
	r.WithRoster(roster)

	p := queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", UserMsgID: "um-1",
		AgentID: "ag-deleted", Channel: domain.ChannelDashboard,
	}
	// What Run does: resolve, install the scope, then complete through it.
	ctx := agentscope.WithScope(context.Background(), scopeOf(r.resolveAgent(context.Background(), p)))
	r.completeWith(ctx, p, "answer", 0, 0, 0, nil, "")

	if got := msgs.appended[0].AgentID; got != "ag-def" {
		t.Errorf("message agent_id = %q, want ag-def — the agent the turn ran as", got)
	}
	if got := msgs.appended[0].AgentID; got == "ag-deleted" {
		t.Error("the message recorded the payload's agent, which no longer exists")
	}
}

// A turn with no roster agent — the eval harness, a company whose seed never
// ran — writes an unattributed row rather than failing. The column is nullable
// for exactly this case, and the repository maps "" to NULL.
func TestAnUnscopedTurnWritesNoAgent(t *testing.T) {
	r, msgs, _ := attributionRunner(t)

	r.completeWith(context.Background(), queue.ChatRunPayload{
		ThreadID: "th-1", UserMsgID: "um-1", Channel: domain.ChannelDashboard,
	}, "answer", 0, 0, 0, nil, "")

	if got := msgs.appended[0].AgentID; got != "" {
		t.Errorf("unscoped turn recorded agent_id %q, want empty", got)
	}
}

// A user message has no author among the agents, and the column must stay
// meaning one thing. If it ever also meant "the agent this was addressed to",
// T-N3's addressing would be unreadable off this table.
func TestAUserMessageCarriesNoAgent(t *testing.T) {
	r, msgs, _ := attributionRunner(t)

	if _, err := r.threads.AppendUserMessage(scoped("ag-fin", "Finance"), "th-1", "what happened?"); err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}

	if got := msgs.appended[0].AgentID; got != "" {
		t.Errorf("user message recorded agent_id %q; a user message has no agent", got)
	}
}

// Every event, not just `final`. The decorator exists so that a publisher added
// later cannot forget, and the only way to pin that is to assert on the ones
// that exist rather than on the helper.
func TestEveryPublishedEventCarriesTheAgent(t *testing.T) {
	r, _, bus := attributionRunner(t)
	ctx := scoped("ag-fin", "Finance")

	_ = r.publish(ctx, "th-1", ChatEvent{Type: "started", Timestamp: time.Now()})
	_ = r.publish(ctx, "th-1", ChatEvent{Type: "delta", Content: "mar"})
	r.completeWith(ctx, queue.ChatRunPayload{
		ThreadID: "th-1", UserMsgID: "um-1", Channel: domain.ChannelDashboard,
	}, "margin fell 4%", 0, 0, 0, nil, "")

	if len(bus.events) < 3 {
		t.Fatalf("published %d events, want at least 3", len(bus.events))
	}
	for _, e := range bus.events {
		if e.AgentID != "ag-fin" || e.AgentName != "Finance" {
			t.Errorf("%s event carried (%q, %q), want (ag-fin, Finance)", e.Type, e.AgentID, e.AgentName)
		}
	}
}

// The negative case for the whole ticket: nothing changes for a turn with no
// roster. A client reading neither field sees the same bytes it saw before.
func TestAnUnscopedTurnPublishesTheSameEventAsBefore(t *testing.T) {
	r, _, bus := attributionRunner(t)

	_ = r.publish(context.Background(), "th-1", ChatEvent{Type: "started"})

	if bus.events[0].AgentID != "" || bus.events[0].AgentName != "" {
		t.Errorf("unscoped event carried (%q, %q), want both empty",
			bus.events[0].AgentID, bus.events[0].AgentName)
	}
}

// The small-talk short-circuit answers without calling the model, and used to
// complete before the agent had been resolved at all. A greeting is still a
// thing an agent said.
func TestAGreetingRecordsWhoGreeted(t *testing.T) {
	r, msgs, _ := attributionRunner(t)
	fin := agentRow("ag-fin", "Finance")
	r.WithRoster(&fakeRoster{byID: map[string]*domain.Agent{"ag-fin": fin}, def: fin})
	r.llmCache = nil

	err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", UserMsgID: "um-1",
		AgentID: "ag-fin", Channel: domain.ChannelDashboard, Message: "hi",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(msgs.appended) != 1 {
		t.Fatalf("appended %d messages, want 1 — did the greeting take the model path?", len(msgs.appended))
	}
	if got := msgs.appended[0].AgentID; got != "ag-fin" {
		t.Errorf("greeting recorded agent_id %q, want ag-fin", got)
	}
}
