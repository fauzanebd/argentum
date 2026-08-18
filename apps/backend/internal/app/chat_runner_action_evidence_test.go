package app

import (
	"context"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/queue"
)

// The transcript T-Q13 was written from: a rename claimed on a turn that called
// nothing at all, on a thread holding no refusal.
func TestAClaimWithNoToolCallIsCounted(t *testing.T) {
	r := &ChatRunner{}
	reply := "Done — your dashboard is now called **Q4 2024 Sales Review**. The URL stays the same."

	got := r.checkActionEvidence(context.Background(), queue.ChatRunPayload{}, reply,
		agentbudget.Snapshot{ToolCalls: 0})
	if got != 1 {
		t.Fatalf("counted %d, want 1 — this is the reply the ticket exists for", got)
	}
}

// The control from the same gate: the same sentence on a turn that did call the
// tool. This is the arm that decides whether the instrument is worth having —
// a counter that fires on the honest turn as well tells nobody anything.
func TestTheControlTurnIsNotCounted(t *testing.T) {
	r := &ChatRunner{}
	reply := "Done — your dashboard is now called **Q4 2024 Sales Review**."

	got := r.checkActionEvidence(context.Background(), queue.ChatRunPayload{}, reply,
		agentbudget.Snapshot{ToolCalls: 1, Tools: []string{"update_dashboard"},
			Succeeded: []string{"update_dashboard"}})
	if got != 0 {
		t.Fatalf("counted %d on a turn that made the call, want 0", got)
	}
}

// A refused call is not evidence. This is T-Q12's sequence seen from the other
// end: the tool never ran, so a reply claiming the work is making the same
// unevidenced claim as one that called nothing.
func TestARefusedMutatingCallIsStillUnevidenced(t *testing.T) {
	r := &ChatRunner{}
	reply := "I've updated the dashboard for you."

	// Tools carries the attempt; Succeeded does not, which is exactly what the
	// budget guard produces when it refuses a call.
	got := r.checkActionEvidence(context.Background(), queue.ChatRunPayload{}, reply,
		agentbudget.Snapshot{ToolCalls: 1, Tools: []string{"update_dashboard"}})
	if got != 1 {
		t.Fatalf("counted %d on a turn whose only mutating call was refused, want 1", got)
	}
}

// A turn that read plenty and changed nothing, then said it changed something.
// The data tools are not evidence of a mutation, which is the distinction the
// mutating marker exists to make.
func TestReadOnlyToolsAreNotEvidenceOfAChange(t *testing.T) {
	r := &ChatRunner{}
	reply := "The dashboard has been renamed."

	got := r.checkActionEvidence(context.Background(), queue.ChatRunPayload{}, reply,
		agentbudget.Snapshot{ToolCalls: 2, Tools: []string{"get_schema", "run_sql"},
			Succeeded: []string{"get_schema", "run_sql"}})
	if got != 1 {
		t.Fatalf("counted %d on a turn that only read, want 1", got)
	}
}

// An ordinary answer claims nothing and must not be counted whatever the turn
// called — including a turn that made no calls at all, which is most turns that
// answer from the conversation.
func TestAnOrdinaryAnswerIsNeverCounted(t *testing.T) {
	r := &ChatRunner{}
	for _, reply := range []string{
		"There were 300 transactions in November 2024.",
		"My exploration budget for this turn was exhausted, so I was not able to rename it.",
		"Which of the two dashboards did you mean?",
	} {
		if got := r.checkActionEvidence(context.Background(), queue.ChatRunPayload{}, reply,
			agentbudget.Snapshot{}); got != 0 {
			t.Errorf("counted %d for %q, want 0", got, reply)
		}
	}
}

// Every write-capable tool in the registry has to be recognised, not just the
// dashboard pair the gate happened to find. This is the check that fails when
// somebody adds a mutating tool and forgets the marker.
func TestTheMutatingSetCoversTheWriteCapableTools(t *testing.T) {
	for _, name := range []string{
		"create_dashboard", "update_dashboard", "schedule_task",
		"generate_document", "propose_action",
	} {
		if !mutatingTools[name] {
			t.Errorf("%s is missing from the mutating set; a claim about it would go uncounted", name)
		}
	}
	for _, name := range []string{"get_schema", "run_sql", "query_metric", "list_sources", "ask_clarification"} {
		if mutatingTools[name] {
			t.Errorf("%s is marked mutating; a read would then excuse a claim about a change", name)
		}
	}
}
