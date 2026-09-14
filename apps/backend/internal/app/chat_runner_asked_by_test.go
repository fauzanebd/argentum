package app

import (
	"context"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-N10's risk 2: a colleague's turn says so on everything it writes, because
// that — and not the agent — is what tells a caller's turn from a colleague's on
// one thread. The agent cannot: a turn whose agent was deleted runs as the
// default, and an agent can be asked back by the colleague it asked.

// Through a whole turn: Run installs the mark from the payload, and the answer
// the colleague writes carries it.
func TestAColleaguesAnswerRecordsWhoAskedForIt(t *testing.T) {
	r, msgs, _ := roomRunner(t, &replyLLM{reply: "One GRN on Tuesday, 200 units."}, &stubParticipants{rows: seats()})

	if err := r.Run(context.Background(), finsQuestion()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(msgs.appended) != 1 {
		t.Fatalf("appended %d messages, want Finance's answer", len(msgs.appended))
	}
	got := msgs.appended[0]
	if got.Metadata[AskedByKey] != "ag-ops" {
		t.Errorf("answer metadata = %v, want asked_by ag-ops", got.Metadata)
	}
	if _, room := got.Metadata[RoomEventKey]; room {
		t.Errorf("an answer was marked as a room line: %v", got.Metadata)
	}
}

// The negative: a turn a person started writes no mark, so every row and event
// before rooms is byte-identical.
func TestAPersonsAnswerRecordsNobodyAsking(t *testing.T) {
	r, msgs, _ := roomRunner(t, &replyLLM{reply: "Stock shows 0 since Tuesday."}, &stubParticipants{rows: seats()})

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
		AgentID: "ag-ops", Message: "What happened to SKU 4471's stock?", UserMsgID: "msg-1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(msgs.appended) != 1 {
		t.Fatalf("appended %d messages, want 1", len(msgs.appended))
	}
	if _, marked := msgs.appended[0].Metadata[AskedByKey]; marked {
		t.Errorf("a person's answer carries asked_by: %v", msgs.appended[0].Metadata)
	}
}

// Every event of a colleague's turn, not only `final`, and none of a person's.
// The agent it ran as is irrelevant to the mark: here Ops is the one asked.
func TestEveryEventOfAColleaguesTurnSaysWhoAsked(t *testing.T) {
	r, msgs, bus := attributionRunner(t)
	colleague := withAskedBy(scoped("ag-ops", "Ops"), "ag-fin")
	steps := []domain.NextStep{{Label: "Check GRN-118", Prompt: "Show GRN-118"}}

	_ = r.publish(colleague, "th-1", ChatEvent{Type: "started", Timestamp: time.Now()})
	_ = r.publish(colleague, "th-1", ChatEvent{Type: "delta", Content: "12 units"})
	r.completeWith(colleague, queue.ChatRunPayload{
		ThreadID: "th-1", UserMsgID: "um-1", Channel: domain.ChannelDashboard,
	}, "12 units short", 0, 0, 0, steps, "", nil)

	if len(bus.events) < 3 {
		t.Fatalf("published %d events, want at least 3", len(bus.events))
	}
	for _, e := range bus.events {
		if e.AskedBy != "ag-fin" || e.AgentID != "ag-ops" {
			t.Errorf("%s event = (agent %q, asked_by %q), want (ag-ops, ag-fin)", e.Type, e.AgentID, e.AskedBy)
		}
	}
	if msgs.appended[0].Metadata[AskedByKey] != "ag-fin" {
		t.Errorf("stored metadata = %v, want asked_by ag-fin", msgs.appended[0].Metadata)
	}
	// The suggestions are still there: the mark is added, never swapped in.
	if _, ok := msgs.appended[0].Metadata["next_steps"]; !ok {
		t.Errorf("stored metadata = %v, lost next_steps", msgs.appended[0].Metadata)
	}

	person, _, personBus := attributionRunner(t)
	_ = person.publish(scoped("ag-ops", "Ops"), "th-1", ChatEvent{Type: "started"})
	if personBus.events[0].AskedBy != "" {
		t.Errorf("a person's turn published asked_by %q", personBus.events[0].AskedBy)
	}
}

// The mark is a copy: a caller's metadata map is not written into.
func TestMarkingAColleaguesAnswerDoesNotWriteIntoTheCallersMap(t *testing.T) {
	r, _, _ := attributionRunner(t)
	meta := map[string]any{"latency_ms": 12}

	if _, err := r.threads.AppendAssistantMessage(withAskedBy(scoped("ag-ops", "Ops"), "ag-fin"),
		"th-1", "12 units short", 0, 0, 0, meta); err != nil {
		t.Fatalf("AppendAssistantMessage: %v", err)
	}

	if _, written := meta[AskedByKey]; written {
		t.Errorf("the caller's map was changed: %v", meta)
	}
}
