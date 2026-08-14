package app

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// The seam these tests cover is the one the eval run walked through twice: the
// runner persists and publishes whatever agent.Run handed back, and until now
// nothing between the two asked whether there was anything there.
//
// What no unit test here can show is why the reply was empty. That needs a live
// turn and the `streaming` field the runner logs beside the replacement.

// trackerAfter returns a tracker that has run the named tools, which is the
// only way to populate a Snapshot — the fields are unexported and Begin is what
// a real turn calls.
func trackerAfter(t *testing.T, tools ...string) *agentbudget.Tracker {
	t.Helper()
	tr := agentbudget.New(agentbudget.Default())
	for _, name := range tools {
		if refusal, blocked := tr.Begin(context.Background(), name); blocked {
			t.Fatalf("the fixture's own budget refused %s: %s", name, refusal)
		}
	}
	return tr
}

// The specimen: three successful calls, a dashboard built, and nothing said
// about it. The user gets a sentence naming the work instead of a blank.
func TestAnEmptyReplyIsReplacedBeforeItIsPersisted(t *testing.T) {
	actions := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(actions)
	tr := trackerAfter(t, "get_schema", "create_visualization", "create_dashboard")

	got := r.rescueEmptyReply(context.Background(), turn(), "", tr, true)

	if strings.TrimSpace(got) == "" {
		t.Fatal("an empty reply reached completeWith")
	}
	if !strings.Contains(got, "create_dashboard") {
		t.Errorf("the replacement does not say what the turn did:\n%s", got)
	}
}

// It is countable, which is the half a log line does not give: two occurrences
// in 58 turns is a rate nobody can measure from prose.
func TestAnEmptyReplyWritesItsOwnAuditRow(t *testing.T) {
	actions := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(actions)

	r.rescueEmptyReply(context.Background(), turn(), "", trackerAfter(t, "run_sql"), true)

	if len(actions.rows) != 1 {
		t.Fatalf("wrote %d audit rows, want 1", len(actions.rows))
	}
	row := actions.rows[0]
	// Not `final_answer`, which is what a refusal writes. Nothing was refused
	// here, and counting this as a guardrail would corrupt the only number that
	// says how often the product declines to answer.
	if row.ToolName != "empty_reply" {
		t.Errorf("audit row tool_name = %q, want empty_reply", row.ToolName)
	}
	if row.ResultStatus != domain.ActionStatusBlocked {
		t.Errorf("audit row result_status = %q, want %q", row.ResultStatus, domain.ActionStatusBlocked)
	}
}

// The ordinary turn, which is every turn: no rewrite and no row.
func TestARealReplyPassesThroughUnaudited(t *testing.T) {
	actions := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(actions)
	const reply = "July's revenue was Rp 1.200.000, up 4% on June."

	got := r.rescueEmptyReply(context.Background(), turn(), reply, trackerAfter(t, "run_sql"), true)

	if got != reply {
		t.Errorf("a real reply was rewritten:\n  got:  %q\n  want: %q", got, reply)
	}
	if len(actions.rows) != 0 {
		t.Errorf("wrote %d audit rows for a healthy turn, want 0", len(actions.rows))
	}
}

// The eval harness runs this same runner with no control-plane repository and
// no tracker on the turn. Both are nil-safe on every other path here, and the
// guard must not be the one that panics — an eval case that crashes the worker
// is worse than the blank answer it is trying to replace.
func TestTheGuardSurvivesAnEvalHarnessRunner(t *testing.T) {
	r := &ChatRunner{}

	got := r.rescueEmptyReply(context.Background(), queue.ChatRunPayload{}, "", nil, false)

	if strings.TrimSpace(got) == "" {
		t.Fatal("the guard returned an empty reply with no tracker attached")
	}
}
