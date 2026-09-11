package app

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// The specimen, captured 2026-09-11 against OpenRouter with `decart` pinned:
// the model's reasoning, then the call it asked for and never got. The turn
// that produced this looked complete — no error, no empty reply, a paragraph
// in the thread — which is why it took a screenshot to notice.
const leakedReply = "I need to check if there's a defined metric for this. Let me call list_metrics " +
	"first, and also get_schema with keywords related to purchases." +
	`functions.list_metrics:0{}functions.get_schema:1{"source_id":"785c6d43-33e1-4d36-a4ce-66058d9da8c3"}`

func TestALeakedToolCallIsReplacedBeforeItIsPersisted(t *testing.T) {
	r := (&ChatRunner{}).WithActionLog(&fakeActionRepo{})

	got, count := r.rejectToolCallLeak(context.Background(), turn(), leakedReply, trackerAfter(t))

	if strings.Contains(got, "functions.list_metrics") {
		t.Errorf("the unexecuted call reached completeWith:\n%s", got)
	}
	// The reasoning is not an answer. Trimming the call and keeping the prose
	// would publish a confident paragraph about work that never ran.
	if strings.Contains(got, "defined metric") {
		t.Errorf("the model's reasoning was published as the answer:\n%s", got)
	}
	if count != 2 {
		t.Errorf("counted %d leaked calls, want 2 — the completion line is what makes this findable", count)
	}
}

func TestALeakedToolCallWritesItsOwnAuditRow(t *testing.T) {
	actions := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(actions)

	r.rejectToolCallLeak(context.Background(), turn(), leakedReply, trackerAfter(t))

	if len(actions.rows) != 1 {
		t.Fatalf("wrote %d audit rows, want 1", len(actions.rows))
	}
	row := actions.rows[0]
	// Its own name, not `final_answer` and not `empty_reply`. This turn was not
	// refused and it was not blank; folding it into either would corrupt the
	// number that says how often each of those happens.
	if row.ToolName != "toolcall_leak" {
		t.Errorf("audit row tool_name = %q, want toolcall_leak", row.ToolName)
	}
	if row.ResultStatus != domain.ActionStatusBlocked {
		t.Errorf("audit row result_status = %q, want %q", row.ResultStatus, domain.ActionStatusBlocked)
	}
}

// A provider that did not decode the tokens at all names no tool, and the turn
// leaked just the same. A zero here would hide it from the only counter that
// can find it.
func TestAnUnnamedLeakStillCountsAsOne(t *testing.T) {
	r := (&ChatRunner{}).WithActionLog(&fakeActionRepo{})

	_, count := r.rejectToolCallLeak(context.Background(), turn(), "Checking.<|tool_call_begin|>", trackerAfter(t))

	if count != 1 {
		t.Errorf("counted %d, want 1", count)
	}
}

func TestAnOrdinaryReplyPassesTheLeakGuardUntouched(t *testing.T) {
	actions := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(actions)
	const reply = "Member dengan pembelanjaan terbesar bulan ini adalah Andi, Rp 12.400.000."

	got, count := r.rejectToolCallLeak(context.Background(), turn(), reply, trackerAfter(t, "run_sql"))

	if got != reply {
		t.Errorf("a real reply was rewritten:\n  got:  %q\n  want: %q", got, reply)
	}
	if count != 0 {
		t.Errorf("counted %d leaked calls on a healthy turn, want 0", count)
	}
	if len(actions.rows) != 0 {
		t.Errorf("wrote %d audit rows for a healthy turn, want 0", len(actions.rows))
	}
}

// Same contract as the empty-reply guard: the eval harness runs this runner
// with no control-plane repository and no tracker, and a guard that panics
// there is worse than the failure it replaces.
func TestTheLeakGuardSurvivesAnEvalHarnessRunner(t *testing.T) {
	r := &ChatRunner{}

	got, count := r.rejectToolCallLeak(context.Background(), queue.ChatRunPayload{}, leakedReply, nil)

	if strings.TrimSpace(got) == "" {
		t.Fatal("the guard returned an empty reply with no tracker attached")
	}
	if count != 2 {
		t.Errorf("counted %d leaked calls, want 2", count)
	}
}
