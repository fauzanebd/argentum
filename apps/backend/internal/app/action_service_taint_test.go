package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/doctaint"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tools"
)

// taintedCtx is the harness context with a turn that has already read one or
// more documents, exactly as search_documents leaves it.
func (h *actionHarness) taintedCtx(sources ...string) context.Context {
	tracker := doctaint.New()
	for _, s := range sources {
		tracker.Mark(s)
	}
	return doctaint.With(h.ctx(), tracker)
}

func (h *actionHarness) proposeIn(t *testing.T, ctx context.Context) *tools.ProposeActionResult {
	t.Helper()
	res, err := h.svc.ProposeAction(ctx, tools.ProposeActionInput{
		Kind: "send_message", Params: json.RawMessage(`{"channel":"whatsapp","target_ref":"+62","body":"hi"}`),
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	return res
}

// The whole ticket in one test: a workspace that auto-approves this kind does
// NOT auto-approve it on a turn that read somebody else's file, and above all
// the action does not run.
func TestTaintedTurnWithholdsAutoApprovalAndExecutesNothing(t *testing.T) {
	h := newActionHarness(t, false) // requires_approval = false: the admin opt-out
	res := h.proposeIn(t, h.taintedCtx("09-scan-invoice.pdf"))

	if !res.RequiresApproval {
		t.Fatal("RequiresApproval = false on a tainted turn — the opt-out survived the taint")
	}
	if res.Status != string(domain.InvocationProposed) {
		t.Fatalf("status = %q, want proposed", res.Status)
	}
	// The one that matters. Everything else in this file is about explaining
	// the decision; this is the decision.
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d — an action ran on a turn that had read an uploaded document", h.act.execCount)
	}
}

// The approver has to be told why a card is in front of them on a kind they
// switched to automatic, or the control reads as a malfunction and gets
// switched off.
func TestTheForcedProposalCarriesItsReason(t *testing.T) {
	h := newActionHarness(t, false)
	h.proposeIn(t, h.taintedCtx("09-scan-invoice.pdf"))

	invs, err := h.repo.ListInvocations(context.Background(), "co-1", 10, 0)
	if err != nil || len(invs) != 1 {
		t.Fatalf("list: %v, %d invocations", err, len(invs))
	}
	reason := invs[0].ApprovalForcedReason
	if reason == "" {
		t.Fatal("approval_forced_reason is empty — the card cannot say why it exists")
	}
	if !strings.Contains(reason, "09-scan-invoice.pdf") {
		t.Fatalf("reason = %q, want it to name the document", reason)
	}
}

// The model relays a sentence to the user, and on this path the plain "needs
// approval" sentence is misleading: the user knows they turned that off.
func TestTheAgentIsToldWhyApprovalWasForced(t *testing.T) {
	h := newActionHarness(t, false)
	res := h.proposeIn(t, h.taintedCtx("09-scan-invoice.pdf"))

	if !strings.Contains(res.Message, "normally runs this action without asking") {
		t.Fatalf("message does not explain the exception:\n%s", res.Message)
	}
	if !strings.Contains(res.Message, "09-scan-invoice.pdf") {
		t.Fatalf("message does not name the document:\n%s", res.Message)
	}
}

// An untainted turn is byte-identical to before the ticket. This is the arm
// that says the gate did not become a blanket approval requirement.
func TestAnUntaintedTurnStillAutoExecutes(t *testing.T) {
	h := newActionHarness(t, false)
	res := h.proposeIn(t, h.ctx())

	if res.RequiresApproval {
		t.Fatal("RequiresApproval = true on an untainted turn — the gate fires on everything")
	}
	if res.Status != string(domain.InvocationExecuted) {
		t.Fatalf("status = %q, want executed", res.Status)
	}
	if h.act.execCount != 1 {
		t.Fatalf("execCount = %d, want 1", h.act.execCount)
	}
	invs, _ := h.repo.ListInvocations(context.Background(), "co-1", 10, 0)
	if len(invs) == 1 && invs[0].ApprovalForcedReason != "" {
		t.Fatalf("an ordinary proposal carries a forced reason: %q", invs[0].ApprovalForcedReason)
	}
}

// A tracker with nothing marked is an untainted turn. The distinction matters
// because search_documents installs a tracker on every turn, marked or not, so
// "a tracker exists" must not mean "a document was read".
func TestAnEmptyTrackerIsNotTaint(t *testing.T) {
	h := newActionHarness(t, false)
	res := h.proposeIn(t, h.taintedCtx()) // tracker present, nothing marked

	if res.RequiresApproval {
		t.Fatal("an unmarked tracker was read as taint")
	}
	if h.act.execCount != 1 {
		t.Fatalf("execCount = %d, want 1", h.act.execCount)
	}
}

// A read with no nameable source still taints — doctaint.Mark records the flag
// either way, and a gate that decided on the filename list would let exactly
// that case through.
func TestAnUnnamedReadStillWithholdsApproval(t *testing.T) {
	h := newActionHarness(t, false)
	res := h.proposeIn(t, h.taintedCtx("")) // Mark("") — tainted, no name

	if !res.RequiresApproval {
		t.Fatal("an unnamed document read did not withhold auto-approval")
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d, want 0", h.act.execCount)
	}
	invs, _ := h.repo.ListInvocations(context.Background(), "co-1", 10, 0)
	if got := invs[0].ApprovalForcedReason; !strings.Contains(got, "uploaded document") {
		t.Fatalf("reason = %q, want it to still say what happened", got)
	}
}

// A kind that already requires approval is untouched: no forced reason, and the
// card it produces is the ordinary one. The gate only ever ADDS a decision.
func TestAKindThatAlreadyNeedsApprovalIsUnchanged(t *testing.T) {
	h := newActionHarness(t, true)
	res := h.proposeIn(t, h.taintedCtx("09-scan-invoice.pdf"))

	if !res.RequiresApproval {
		t.Fatal("RequiresApproval = false")
	}
	invs, _ := h.repo.ListInvocations(context.Background(), "co-1", 10, 0)
	if got := invs[0].ApprovalForcedReason; got != "" {
		t.Fatalf("forced reason = %q on a kind that needed approval anyway", got)
	}
	if strings.Contains(res.Message, "normally runs this action without asking") {
		t.Fatalf("the exception sentence appeared on an ordinary approval:\n%s", res.Message)
	}
}

func TestTaintApprovalReasonWording(t *testing.T) {
	mk := func(sources ...string) context.Context {
		tr := doctaint.New()
		for _, s := range sources {
			tr.Mark(s)
		}
		return doctaint.With(context.Background(), tr)
	}

	if got := taintApprovalReason(context.Background()); got != "" {
		t.Fatalf("no tracker gave a reason: %q", got)
	}
	if got := taintApprovalReason(mk("a.pdf")); !strings.Contains(got, "the uploaded document a.pdf") {
		t.Fatalf("one document: %q", got)
	}
	if got := taintApprovalReason(mk("a.pdf", "b.pdf")); !strings.Contains(got, "a.pdf and b.pdf") {
		t.Fatalf("two documents: %q", got)
	}
	got := taintApprovalReason(mk("a.pdf", "b.pdf", "c.pdf"))
	if !strings.Contains(got, "3 uploaded documents") || !strings.Contains(got, "including") {
		t.Fatalf("three documents: %q", got)
	}
}

// The filenames are tenant-supplied and travel to an approval card, a log line
// and a security review. A 4 KB one must not.
func TestTheReasonIsBoundedAndCutsOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("ä", 500) + ".pdf" // multi-byte, deliberately
	tr := doctaint.New()
	tr.Mark(long)
	got := taintApprovalReason(doctaint.With(context.Background(), tr))

	if len(got) > maxTaintReasonBytes {
		t.Fatalf("reason is %d bytes, over the %d cap", len(got), maxTaintReasonBytes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a truncated reason does not say it was truncated: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("the cut landed inside a rune: %q", got)
	}
}
