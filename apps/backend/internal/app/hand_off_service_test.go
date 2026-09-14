package app

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/taint"
	"github.com/fauzanebd/argentum/internal/tools"
)

// T-N7: hand_off_to_agent's rules, on nudgeFixture's room — Ops the default
// speaker, Finance added, People on the roster and not in the room — over the
// same real ledger nudge_agent's tests use.

const (
	writeOffs     = "what did we write off for SKU 4471 last quarter?"
	becauseLedger = "Write-offs are booked in Finance's ledger."
)

// handing is the person's own message to Ops, about something that is Finance's.
func (f *nudgeRoom) handing() queue.ChatRunPayload {
	p := f.asking()
	p.Message = writeOffs
	return p
}

func TestAHandOffWritesTheReplyAndSendsThePersonsOwnWords(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	ctx := f.in(f.handing())
	taint.Mark(ctx, taint.KindDocument, "writeoff-policy.pdf")

	res := decoded(t, f.svc.HandOff(ctx, "@finance", becauseLedger))

	if res["handed_off"] != true {
		t.Fatalf("result = %v, want handed_off", res)
	}
	if len(f.notes.notes) != 1 {
		t.Fatalf("lines written = %d, want 1", len(f.notes.notes))
	}
	line := f.notes.notes[0]
	if line.content != "Passed to Finance: "+becauseLedger || line.agentID != "ag-ops" || line.meta[HandedOffToKey] != "ag-fin" {
		t.Errorf("the reply = %+v, want Ops saying it passed the question to Finance", line)
	}
	if isRoomEvent(line.meta) {
		t.Error("the hand-off was stored as a room line, which no reader counts as the turn's answer")
	}

	if len(f.queue.payloads) != 1 {
		t.Fatalf("queued %d turns, want exactly 1", len(f.queue.payloads))
	}
	got := f.queue.payloads[0]
	if got.AgentID != "ag-fin" || got.Message != writeOffs {
		t.Errorf("Finance was sent %q as agent %q — want the person's own words", got.Message, got.AgentID)
	}
	if got.Peer == nil || got.Peer.HandOff == nil || got.Peer.HandOff.Reason != becauseLedger {
		t.Fatalf("peer = %+v, want the reason attached as a hand-off", got.Peer)
	}
	if got.Peer.AgentID != "ag-ops" || got.Peer.ParticipantID != "tp-fin" || got.Peer.Depth != 1 {
		t.Errorf("peer = %+v, want Ops, pinned to tp-fin, at depth 1", got.Peer)
	}
	if len(got.Peer.Taint[taint.KindDocument]) != 1 {
		t.Error("the document the handing turn read did not cross the hand-off")
	}
	if got.Directive != "" || got.APIReportID != "" || got.ScheduledRunID != "" || got.WatcherEventID != "" {
		t.Errorf("the handed-off turn carries what the handing turn was for: %+v", got)
	}
	if h := handedOffIn(ctx); h == nil || h.to != "Finance" || h.content != line.content || h.message == nil {
		t.Errorf("the turn does not know it handed off: %+v", h)
	}
}

// "Gated by the same can_nudge flag, the same participant rule": nudge_agent's
// refusals, reached through the hand-off, and its own two.
func TestAHandOffThatCannotBeMadeIsRefusedAndSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		name, agent, reason, code string
	}{
		{"a roster agent not in the room", "People", becauseLedger, "not_in_conversation"},
		{"itself", "Ops", becauseLedger, "cannot_ask_yourself"},
		{"a disabled agent", "Retired", becauseLedger, "agent_disabled"},
		{"nobody named", "  ", becauseLedger, "missing_agent"},
		{"no reason", "Finance", " ", "missing_reason"},
		{"a paraphrase for a reason", "Finance", strings.Repeat("x", tools.HandOffReasonMax+1), "reason_too_long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := nudgeFixture(t, agentbudget.Ceilings{})
			off := agentRow("ag-off", "Retired")
			off.Enabled = false
			f.roster.byID["ag-off"] = off
			f.room.rows = append(f.room.rows, &domain.ThreadParticipant{ID: "tp-off", AgentID: "ag-off", AgentName: "Retired"})
			ctx := f.in(f.handing())

			res := decoded(t, f.svc.HandOff(ctx, tc.agent, tc.reason))

			if res["error"] != tc.code {
				t.Errorf("refusal = %v, want %s", res, tc.code)
			}
			if len(f.queue.payloads) != 0 || len(f.notes.notes) != 0 || len(f.mr.Keys()) != 0 {
				t.Errorf("a refused hand-off queued %d, wrote %d, touched the ledger %v",
					len(f.queue.payloads), len(f.notes.notes), f.mr.Keys())
			}
			if handedOffIn(ctx) != nil {
				t.Error("a refused hand-off ended the turn")
			}
		})
	}
}

func TestTheHandOffsGatesAreCheckedWhenItRuns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*nudgeRoom) queue.ChatRunPayload
		note  string
	}{
		{"the agent may no longer nudge", func(f *nudgeRoom) queue.ChatRunPayload {
			f.roster.byID["ag-ops"].CanNudge = false
			return f.handing()
		}, ""},
		{"the room is down to one", func(f *nudgeRoom) queue.ChatRunPayload {
			f.room.rows = f.room.rows[:1]
			return f.handing()
		}, ""},
		// T-N7's own gate. Ops asked Finance something; Finance holds Ops' words, not
		// the person's, and is pointed at the pass and at nudge_agent instead.
		{"a colleague's question", func(f *nudgeRoom) queue.ChatRunPayload {
			p := f.handing()
			p.AgentID, p.Message = "ag-fin", goodsIn
			p.Peer = &queue.PeerOrigin{AgentID: "ag-ops", ParticipantID: "tp-fin", Depth: 1}
			return p
		}, "reply PASS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := nudgeFixture(t, agentbudget.Ceilings{})
			target := "Finance"
			p := tc.setup(f)
			if p.AgentID == "ag-fin" {
				target = "Ops"
			}
			res := decoded(t, f.svc.HandOff(f.in(p), target, becauseLedger))
			note, _ := res["note"].(string)
			if res["error"] != "not_available" || len(f.queue.payloads) != 0 || !strings.Contains(note, tc.note) {
				t.Errorf("result = %v with %d queued, want not_available and nothing queued", res, len(f.queue.payloads))
			}
		})
	}
}

// The acceptance line reads "a hand-off back to the original agent is refused by
// the depth counter, not by a special case". At the default depth it is refused
// one hop earlier, by the ledger's repeat check: the question handed back is the
// very words Open recorded Ops as asked. Both are T-N8's ledger, and neither is a
// rule about going back.
func TestAHandOffBackIsRefusedByTheLedger(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	if err := f.ledger.Open(context.Background(), "co-1", "msg-1", writeOffs, []string{"ag-ops"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.svc.HandOff(f.in(f.handing()), "Finance", becauseLedger)
	if len(f.queue.payloads) != 1 {
		t.Fatal("the first hand-off was not queued")
	}

	// Finance, handed the question, hands it straight back.
	back := decoded(t, f.svc.HandOff(f.in(f.queue.payloads[0]), "Ops", "Stock write-offs are Ops' to explain."))

	if back["error"] != "already_asked" {
		t.Errorf("handing it back = %v, want the ledger's repeat", back)
	}
	if len(f.queue.payloads) != 1 || len(f.notes.notes) != 1 {
		t.Errorf("handing it back queued %d turns and wrote %d lines in all; want the first hand-off alone",
			len(f.queue.payloads), len(f.notes.notes))
	}
}

// And the depth counter is what stops a chain whose words the ledger has not seen
// against that agent: a third hop, whatever tool makes it.
func TestAThirdHopHandOffIsRefusedOnDepth(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.room.rows = append(f.room.rows, &domain.ThreadParticipant{ID: "tp-hr", AgentID: "ag-hr", AgentName: "People"})
	p := f.handing()
	p.AgentID = "ag-fin"
	p.Peer = &queue.PeerOrigin{AgentID: "ag-ops", ParticipantID: "tp-fin", Depth: 2, HandOff: &queue.HandOff{Reason: becauseLedger}}

	res := decoded(t, f.svc.HandOff(f.in(p), "People", "Staff equipment write-offs are People's."))

	if res["budget_exhausted"] != true || len(f.queue.payloads) != 0 {
		t.Errorf("a depth-3 hand-off = %v with %d queued, want refused", res, len(f.queue.payloads))
	}
	unasked := f.notes.of(RoomEventUnasked)
	if len(unasked) != 1 || !strings.Contains(unasked[0].content, "Finance could not hand the question to People") ||
		!strings.Contains(unasked[0].content, writeOffs) {
		t.Errorf("room lines = %+v, want one saying the hand-off to People did not happen, quoting the question", unasked)
	}
}

func TestAHandOffCountsAgainstTheConversationBudget(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{MaxAgentTurns: 2})
	if err := f.ledger.Open(context.Background(), "co-1", "msg-1", writeOffs, []string{"ag-ops"}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	f.svc.HandOff(f.in(f.handing()), "Finance", becauseLedger)

	if got := f.mr.HGet("conv:budget:co-1:msg-1", "turns"); got != "2" {
		t.Errorf("the conversation counted %s turns, want 2 — the person's and the hand-off", got)
	}
	// So the next ask from this message's fan-out finds the ceiling reached.
	res := decoded(t, f.svc.Nudge(f.in(f.queue.payloads[0]), "Ops", "How many units of SKU 4471 are on hand?"))
	if res["budget_exhausted"] != true {
		t.Errorf("an ask after the hand-off = %v, want the conversation budget's refusal", res)
	}
}

// One question, one colleague: a turn hands its question on once, and asks
// nobody about it afterwards.
func TestATurnHandsItsQuestionOnOnce(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.room.rows = append(f.room.rows, &domain.ThreadParticipant{ID: "tp-hr", AgentID: "ag-hr", AgentName: "People"})
	ctx := f.in(f.handing())

	f.svc.HandOff(ctx, "Finance", becauseLedger)
	again := decoded(t, f.svc.HandOff(ctx, "People", "Or perhaps People's."))
	asked := decoded(t, f.svc.Nudge(ctx, "People", "Was SKU 4471 staff equipment?"))

	if again["error"] != "already_handed_off" {
		t.Errorf("a second hand-off = %v, want already_handed_off", again)
	}
	if asked["error"] != "handed_off" {
		t.Errorf("a nudge after handing off = %v, want handed_off", asked)
	}
	if len(f.queue.payloads) != 1 || len(f.notes.notes) != 1 {
		t.Errorf("queued %d, wrote %d; want the one hand-off", len(f.queue.payloads), len(f.notes.notes))
	}
}

// A hand-off whose turn could not be queued has handed nothing over. The model is
// told, and the turn is not ended — otherwise the person's question would be
// answered by nobody.
func TestAHandOffThatWasNotQueuedLeavesTheQuestionWithTheTurn(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.queue.failAt = 1
	ctx := f.in(f.handing())

	res := decoded(t, f.svc.HandOff(ctx, "Finance", becauseLedger))

	if res["error"] != "not_delivered" {
		t.Errorf("result = %v, want not_delivered", res)
	}
	if handedOffIn(ctx) != nil {
		t.Error("a hand-off that was never queued ended the turn")
	}
}

func TestATenantAtZeroCannotHandOff(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.svc.WithBudget(nudgeCredits{state: BudgetState{Verdict: BudgetExhausted}})

	res := decoded(t, f.svc.HandOff(f.in(f.handing()), "Finance", becauseLedger))

	if res["error"] != "credits_exhausted" || len(f.queue.payloads) != 0 || len(f.mr.Keys()) != 0 {
		t.Errorf("result = %v, queued %d, ledger %v; want the credit refusal and nothing else",
			res, len(f.queue.payloads), f.mr.Keys())
	}
}
