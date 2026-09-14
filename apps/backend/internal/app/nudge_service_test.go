package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/taint"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools"
)

// T-N6: nudge_agent's rules, one test each. The service is driven directly with
// the context a turn would carry, over a real ledger on miniredis — the loop
// guard is the thing most worth not faking.

// stubParticipants is a room. It counts reads, so a test can say none happened.
type stubParticipants struct {
	rows  []*domain.ThreadParticipant
	err   error
	reads int
}

func (s *stubParticipants) ListParticipants(context.Context, string, string) ([]*domain.ThreadParticipant, error) {
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	return append([]*domain.ThreadParticipant(nil), s.rows...), nil
}

// roomNote is one line written into the room, with the agent it was written as.
type roomNote struct {
	agentID string
	content string
	meta    map[string]any
}

type recordingNotes struct {
	mu    sync.Mutex
	notes []roomNote
	err   error
}

func (r *recordingNotes) AppendAssistantMessage(
	ctx context.Context, _, content string, _, _ int, _ int64, meta map[string]any,
) (*domain.Message, error) {
	if r.err != nil {
		return nil, r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, roomNote{agentID: agentscope.AgentID(ctx), content: content, meta: meta})
	return &domain.Message{ID: fmt.Sprintf("note-%d", len(r.notes))}, nil
}

func (r *recordingNotes) of(kind string) []roomNote {
	var out []roomNote
	for _, n := range r.notes {
		if n.meta[RoomEventKey] == kind {
			out = append(out, n)
		}
	}
	return out
}

type nudgeCredits struct {
	state BudgetState
	err   error
}

func (c nudgeCredits) CheckBudget(context.Context, string) (BudgetState, error) {
	return c.state, c.err
}

type nudgeRoom struct {
	svc    *NudgeService
	roster *fakeRoster
	room   *stubParticipants
	notes  *recordingNotes
	queue  *recordingEnqueuer
	ledger *agentbudget.Conversation
	mr     *miniredis.Miniredis
}

// nudgeFixture is a room of two — Ops, the default speaker with no membership
// row, and Finance — in which both may ask. People is on the roster and not in
// the room.
func nudgeFixture(t *testing.T, c agentbudget.Ceilings) *nudgeRoom {
	t.Helper()
	ops := agentRow("ag-ops", "Ops")
	ops.CanNudge = true
	fin := agentRow("ag-fin", "Finance")
	fin.CanNudge = true
	hr := agentRow("ag-hr", "People")
	f := &nudgeRoom{
		roster: &fakeRoster{byID: map[string]*domain.Agent{"ag-ops": ops, "ag-fin": fin, "ag-hr": hr}, def: ops},
		room: &stubParticipants{rows: []*domain.ThreadParticipant{
			{ThreadID: "th-1", AgentID: "ag-ops", AgentName: "Ops"},
			{ID: "tp-fin", ThreadID: "th-1", AgentID: "ag-fin", AgentName: "Finance"},
		}},
		notes: &recordingNotes{},
		queue: &recordingEnqueuer{},
		mr:    miniredis.RunT(t),
	}
	f.ledger = ledgerOver(t, f.mr.Addr(), c)
	f.svc = NewNudgeService(f.roster, f.room, f.notes, f.queue, f.ledger)
	return f
}

// asking is the turn the person's message started: Ops, answering on the
// dashboard. It carries fields that belong to the asking turn alone, so a test
// can see them cleared on the asked one.
func (f *nudgeRoom) asking() queue.ChatRunPayload {
	return queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", UserID: "user-1", Channel: domain.ChannelDashboard,
		Message: "we're short on SKU 4471, what happened?", AgentID: "ag-ops", UserMsgID: "msg-1",
		CompanyName: "Toko Maju", DefaultCurrency: "IDR", RequestID: "req-1",
		Directive: "a report directive", APIReportID: "rep-1", ScheduledRunID: "run-1", WatcherEventID: "we-1",
	}
}

// in is the context nudge_agent runs in: the turn p, running as agentID.
func (f *nudgeRoom) in(p queue.ChatRunPayload) context.Context {
	ctx := tenantctx.WithCompanyID(context.Background(), p.CompanyID)
	ctx = tenantctx.WithThreadID(ctx, p.ThreadID)
	name := ""
	if a, ok := f.roster.byID[p.AgentID]; ok {
		name = a.Name
	}
	ctx = agentscope.WithScope(ctx, agentscope.Scope{AgentID: p.AgentID, Name: name})
	ctx = taint.With(ctx, taint.New())
	return withAskingTurn(ctx, p)
}

func decoded(t *testing.T, result string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("result is not JSON: %q", result)
	}
	return m
}

const goodsIn = "Was a goods-in posted for SKU 4471 after Monday?"

func TestANudgeWritesOneVisibleQuestionAndQueuesOneTurn(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})

	res := decoded(t, f.svc.Nudge(f.in(f.asking()), "finance", goodsIn))

	if res["asked"] != true {
		t.Fatalf("result = %v, want asked", res)
	}
	if len(f.notes.notes) != 1 {
		t.Fatalf("room lines = %d, want 1", len(f.notes.notes))
	}
	note := f.notes.notes[0]
	if note.content != "→ Finance: "+goodsIn || note.agentID != "ag-ops" || note.meta[RoomEventKey] != RoomEventNudge {
		t.Errorf("the question in the room = %+v, want Ops' message addressed to Finance", note)
	}
	if len(f.queue.payloads) != 1 {
		t.Fatalf("queued %d turns, want exactly 1", len(f.queue.payloads))
	}
	got := f.queue.payloads[0]
	if got.AgentID != "ag-fin" || got.Message != goodsIn || got.UserMsgID != "msg-1" {
		t.Errorf("queued turn = agent %q, message %q, user message %q", got.AgentID, got.Message, got.UserMsgID)
	}
	if got.Peer == nil || got.Peer.AgentID != "ag-ops" || got.Peer.ParticipantID != "tp-fin" || got.Peer.Depth != 1 {
		t.Errorf("peer = %+v, want Ops, pinned to tp-fin, at depth 1", got.Peer)
	}
	// What the answer is for is the conversation's, not the asking turn's.
	if got.UserID != "user-1" || got.Channel != domain.ChannelDashboard || got.CompanyName != "Toko Maju" ||
		got.DefaultCurrency != "IDR" || got.RequestID != "req-1" {
		t.Errorf("the asked turn lost the conversation's context: %+v", got)
	}
	if got.Directive != "" || got.APIReportID != "" || got.ScheduledRunID != "" || got.WatcherEventID != "" {
		t.Errorf("the asked turn carries what the asking turn was for: %+v", got)
	}
}

// The default speaker has no membership row, and is pinned by the empty id — the
// shape recipientLeft checks.
func TestTheDefaultSpeakerIsPinnedByTheEmptyMembership(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	p := f.asking()
	p.AgentID = "ag-fin"

	f.svc.Nudge(f.in(p), "Ops", "How many units of SKU 4471 did we receive?")

	if len(f.queue.payloads) != 1 || f.queue.payloads[0].Peer.ParticipantID != "" || f.queue.payloads[0].AgentID != "ag-ops" {
		t.Fatalf("queued = %+v, want one turn for Ops pinned to the default speaker's seat", f.queue.payloads)
	}
}

func TestANudgeThatCannotBeAskedIsRefusedAndNamesWhoCan(t *testing.T) {
	for _, tc := range []struct {
		name, agent, question, code string
		names                       bool
	}{
		{"a roster agent not in the room", "People", goodsIn, "not_in_conversation", true},
		{"a name nobody has", "Legal", goodsIn, "not_in_conversation", true},
		{"another company's agent in the list", "Auditor", goodsIn, "not_in_conversation", true},
		{"itself", "Ops", goodsIn, "cannot_ask_yourself", true},
		{"a disabled agent", "Retired", goodsIn, "agent_disabled", true},
		{"nobody named", "  ", goodsIn, "missing_agent", true},
		{"no question", "Finance", " ", "missing_question", false},
		{"a transcript for a question", "Finance", strings.Repeat("x", tools.NudgeQuestionMax+1), "question_too_long", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := nudgeFixture(t, agentbudget.Ceilings{})
			off := agentRow("ag-off", "Retired")
			off.Enabled = false
			other := agentRow("ag-x", "Auditor")
			other.CompanyID = "co-2"
			f.roster.byID["ag-off"], f.roster.byID["ag-x"] = off, other
			f.room.rows = append(f.room.rows,
				&domain.ThreadParticipant{ID: "tp-off", AgentID: "ag-off", AgentName: "Retired"},
				&domain.ThreadParticipant{ID: "tp-x", AgentID: "ag-x", AgentName: "Auditor"},
			)

			res := decoded(t, f.svc.Nudge(f.in(f.asking()), tc.agent, tc.question))

			if res["error"] != tc.code {
				t.Errorf("refusal = %v, want %s", res, tc.code)
			}
			note, _ := res["note"].(string)
			if tc.names && !strings.Contains(note, "You can ask: Finance") {
				t.Errorf("the refusal does not name who can be asked: %q", note)
			}
			if len(f.queue.payloads) != 0 || len(f.notes.notes) != 0 {
				t.Errorf("a refused nudge queued %d turn(s) and wrote %d line(s)", len(f.queue.payloads), len(f.notes.notes))
			}
			if keys := f.mr.Keys(); len(keys) != 0 {
				t.Errorf("a refused nudge touched the ledger: %v", keys)
			}
		})
	}
}

// Decision 8's gates, again at dispatch: the tool list was built when the turn
// started, and the room or the flag can have changed since.
func TestTheGatesAreCheckedAgainWhenTheToolRuns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*nudgeRoom) context.Context
	}{
		{"the agent may no longer nudge", func(f *nudgeRoom) context.Context {
			f.roster.byID["ag-ops"].CanNudge = false
			return f.in(f.asking())
		}},
		{"the room is down to one", func(f *nudgeRoom) context.Context {
			f.room.rows = f.room.rows[:1]
			return f.in(f.asking())
		}},
		{"no turn on the context", func(f *nudgeRoom) context.Context {
			return agentscope.WithScope(tenantctx.WithCompanyID(context.Background(), "co-1"),
				agentscope.Scope{AgentID: "ag-ops"})
		}},
		{"an unscoped turn", func(f *nudgeRoom) context.Context {
			return withAskingTurn(tenantctx.WithCompanyID(context.Background(), "co-1"), f.asking())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := nudgeFixture(t, agentbudget.Ceilings{})
			res := decoded(t, f.svc.Nudge(tc.setup(f), "Finance", goodsIn))
			if res["error"] != "not_available" || len(f.queue.payloads) != 0 {
				t.Errorf("result = %v with %d queued, want not_available and nothing queued", res, len(f.queue.payloads))
			}
		})
	}
}

// T-N8's note: the credit check is not the loop guard, and a tenant at zero reads
// the credit refusal — with the ledger never asked.
func TestATenantAtZeroReadsTheCreditRefusal(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.svc.WithBudget(nudgeCredits{state: BudgetState{Verdict: BudgetExhausted}})

	res := decoded(t, f.svc.Nudge(f.in(f.asking()), "Finance", goodsIn))

	note, _ := res["note"].(string)
	if res["error"] != "credits_exhausted" || !strings.Contains(note, CreditsExhaustedMessage) {
		t.Errorf("result = %v, want the credit refusal", res)
	}
	if _, loopGuard := res["budget_exhausted"]; loopGuard {
		t.Error("a tenant at zero read the conversation budget's refusal")
	}
	if keys := f.mr.Keys(); len(keys) != 0 {
		t.Errorf("the ledger was asked for a tenant at zero: %v", keys)
	}
	if len(f.queue.payloads) != 0 || len(f.notes.notes) != 0 {
		t.Error("a nudge from a tenant at zero queued or wrote something")
	}
}

func TestTheSameQuestionToTheSameColleagueIsAskedOnce(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	ctx := f.in(f.asking())

	f.svc.Nudge(ctx, "Finance", goodsIn)
	res := decoded(t, f.svc.Nudge(ctx, "Finance", "  was a GOODS-IN posted for sku 4471 after monday?"))

	if res["already_asked"] != true {
		t.Errorf("second ask = %v, want already_asked", res)
	}
	if len(f.queue.payloads) != 1 || len(f.notes.notes) != 1 {
		t.Errorf("queued %d, wrote %d; want the question asked and shown once", len(f.queue.payloads), len(f.notes.notes))
	}
}

// The room hears about a refused question once per agent per person's message —
// the decision T-N8 left here.
func TestARefusedQuestionIsToldToTheRoomOncePerAgent(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{MaxAgentTurns: 1})
	ops := f.in(f.asking())

	f.svc.Nudge(ops, "Finance", goodsIn) // the one turn the budget has
	first := decoded(t, f.svc.Nudge(ops, "Finance", "And for SKU 4472?"))
	f.svc.Nudge(ops, "Finance", "And for SKU 4473?")

	if first["budget_exhausted"] != true {
		t.Fatalf("second ask = %v, want the conversation budget's refusal", first)
	}
	unasked := f.notes.of(RoomEventUnasked)
	if len(unasked) != 1 {
		t.Fatalf("Ops' refusals wrote %d room lines, want 1", len(unasked))
	}
	if !strings.Contains(unasked[0].content, `"And for SKU 4472?"`) || unasked[0].meta["dimension"] != "turns" {
		t.Errorf("the notice = %+v, want it to quote the unasked question and name the limit", unasked[0])
	}

	fin := f.asking()
	fin.AgentID = "ag-fin"
	f.svc.Nudge(f.in(fin), "Ops", "What did we sell of SKU 4471?")
	if got := len(f.notes.of(RoomEventUnasked)); got != 2 {
		t.Errorf("Finance's refusal wrote no line of its own (%d lines) — the person would not hear about it", got)
	}
	if len(f.queue.payloads) != 1 {
		t.Errorf("queued %d turns against a budget of one", len(f.queue.payloads))
	}
}

// Admit fails closed and the room still hears about it: an outage must not hide
// a refusal from the person.
func TestALedgerThatCannotBeReadRefusesAndStillTellsTheRoom(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.svc.ledger = agentbudget.NewConversation(nil, agentbudget.Ceilings{})

	res := decoded(t, f.svc.Nudge(f.in(f.asking()), "Finance", goodsIn))

	if res["budget_exhausted"] != true || len(f.queue.payloads) != 0 {
		t.Errorf("result = %v with %d queued, want refused and nothing queued", res, len(f.queue.payloads))
	}
	if len(f.notes.of(RoomEventUnasked)) != 1 {
		t.Error("an outage refusal was not told to the room")
	}
}

func TestAThirdHopIsRefusedOnDepth(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	p := f.asking()
	p.Peer = &queue.PeerOrigin{AgentID: "ag-fin", Depth: 2}

	res := decoded(t, f.svc.Nudge(f.in(p), "Finance", goodsIn))

	if res["budget_exhausted"] != true || len(f.queue.payloads) != 0 {
		t.Errorf("a depth-3 ask = %v with %d queued, want refused", res, len(f.queue.payloads))
	}
}

// The acceptance line T-N5 could only prove by hand: a nudge from a turn that had
// read an uploaded document produces a colleague's turn that gates
// propose_action under T-H9 — through the payload this service writes, and the
// runner's receiving half and the action service's own gate.
func TestANudgeFromATurnThatReadADocumentGatesTheColleaguesActions(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	ctx := f.in(f.asking())
	taint.Mark(ctx, taint.KindDocument, "invoice-4471.pdf")

	f.svc.Nudge(ctx, "Finance", goodsIn)
	if len(f.queue.payloads) != 1 {
		t.Fatal("nothing was queued")
	}

	h := newActionHarness(t, false) // an admin opted this kind out of approval
	runner, _ := rosterFixture()
	tr := taint.New()
	colleague := taint.With(h.ctx(), tr)
	runner.receivePeer(colleague, tr, f.queue.payloads[0])
	res := h.proposeIn(t, colleague)

	if !res.RequiresApproval || h.act.execCount != 0 {
		t.Fatalf("RequiresApproval = %v, executed %d — the document crossed the nudge ungated", res.RequiresApproval, h.act.execCount)
	}
}

// slowColleague is the queue with a worker behind it: the colleague's turn starts
// when the question is queued and takes a second.
type slowColleague struct {
	done chan struct{}
}

func (q *slowColleague) EnqueueChatRun(context.Context, queue.ChatRunPayload) (string, error) {
	go func() {
		time.Sleep(time.Second)
		close(q.done)
	}()
	return "task-1", nil
}

// "Measured, not assumed": the asking turn gets its result back while the
// colleague's turn is still running.
func TestTheAskingTurnDoesNotWaitForTheAnswer(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	colleague := &slowColleague{done: make(chan struct{})}
	f.svc.enqueuer = colleague

	start := time.Now()
	res := decoded(t, f.svc.Nudge(f.in(f.asking()), "Finance", goodsIn))
	elapsed := time.Since(start)

	select {
	case <-colleague.done:
		t.Fatal("nudge_agent returned only after the colleague's turn had finished")
	default:
	}
	if res["asked"] != true || elapsed > 500*time.Millisecond {
		t.Errorf("result = %v after %s, want asked well inside the colleague's second", res, elapsed)
	}
	t.Logf("nudge_agent returned in %s; the colleague's turn was still running", elapsed)
	<-colleague.done
}

// T-N8's first acceptance line, which moved here: a room in which every turn
// asks every colleague something new ends — and the room says why.
func TestARoomThatAlwaysNudgesEndsAndSaysWhy(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.roster.byID["ag-hr"].CanNudge = true
	f.room.rows = append(f.room.rows, &domain.ThreadParticipant{ID: "tp-hr", AgentID: "ag-hr", AgentName: "People"})
	room := []struct{ id, name string }{{"ag-ops", "Ops"}, {"ag-fin", "Finance"}, {"ag-hr", "People"}}

	// The person's own message, to Ops, is on the ledger before anything asks.
	if err := f.ledger.Open(context.Background(), "co-1", "msg-1", f.asking().Message, []string{"ag-ops"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	pending, seen, turns, asked := []queue.ChatRunPayload{f.asking()}, 0, 0, 0
	for len(pending) > 0 {
		if turns > 100 {
			t.Fatal("the room did not stop")
		}
		p := pending[0]
		pending = pending[1:]
		turns++
		for _, colleague := range room {
			if colleague.id == p.AgentID {
				continue
			}
			asked++
			f.svc.Nudge(f.in(p), colleague.name, fmt.Sprintf("Question %d?", asked))
		}
		pending = append(pending, f.queue.payloads[seen:]...)
		seen = len(f.queue.payloads)
	}

	if ceiling := agentbudget.DefaultCeilings().MaxAgentTurns; turns > ceiling {
		t.Errorf("%d turns ran from one message, past the ceiling of %d", turns, ceiling)
	}
	unasked := f.notes.of(RoomEventUnasked)
	if len(unasked) == 0 {
		t.Fatal("the room stopped and said nothing about why")
	}
	byAgent := map[string]int{}
	for _, n := range unasked {
		byAgent[n.agentID]++
		if !strings.Contains(n.content, "went unasked") || !strings.Contains(n.content, `"Question `) {
			t.Errorf("a notice does not name the unasked question: %q", n.content)
		}
	}
	for agent, n := range byAgent {
		if n > 1 {
			t.Errorf("%s wrote %d limit notices into one message's room", agent, n)
		}
	}
	t.Logf("%d turns ran, %d questions asked, %d limit notice(s): %v", turns, asked, len(unasked), byAgent)
}

func TestAQuestionThatCannotBeWrittenIsNotQueued(t *testing.T) {
	f := nudgeFixture(t, agentbudget.Ceilings{})
	f.notes.err = errors.New("control database is down")

	res := decoded(t, f.svc.Nudge(f.in(f.asking()), "Finance", goodsIn))

	if res["error"] != "not_delivered" || len(f.queue.payloads) != 0 {
		t.Errorf("result = %v with %d queued; an answer to a question nobody can see is decision 6's side channel", res, len(f.queue.payloads))
	}
}
