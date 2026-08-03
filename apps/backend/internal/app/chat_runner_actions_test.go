package app

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The action catalog at the turn (the T-12b follow-up).
//
// The finding this closes: `T-12b` shipped a capability a tenant could enable,
// configure, and never reach. `propose_action`'s description is one static
// string shared by every tenant, so it can name one example — send_message — and
// nothing about what *this* workspace turned on. The 2026-08-02 gate measured
// the cost: four turns tried to reach http_action and one landed, the one whose
// user message dictated the tool arguments.
//
// The rule under every case here is the one withMetricsContext already follows:
// the block makes a turn better and never makes a turn possible. A company with
// nothing enabled, a deployment that never wired it, and a read that fails all
// have to produce exactly the turn this product had before the block existed.

type stubActionCatalog struct {
	entries []ActionCatalogEntry
	err     error
	calls   int
}

func (s *stubActionCatalog) CatalogForTurn(context.Context, string) ([]ActionCatalogEntry, error) {
	s.calls++
	return s.entries, s.err
}

func TestActionsBlockNamesTheKindsAndTheirParams(t *testing.T) {
	cat := &stubActionCatalog{entries: []ActionCatalogEntry{
		{Kind: "send_message", Usage: `params: {"channel": "whatsapp", "target_ref": "…", "body": "…"}`, RequiresApproval: true},
		{Kind: "http_action", Usage: `params: {"endpoint": "<name>", "params": {…}}`, RequiresApproval: true,
			Options: []string{"ops_ticket", "refund_request"}},
	}}
	r := (&ChatRunner{}).WithActionCatalog(cat)

	got := r.withActionsContext(context.Background(), "file a ticket", "co-1")

	for _, want := range []string{
		"send_message", "http_action", "propose_action",
		`"target_ref"`, `"endpoint"`,
		// The half a static tool description cannot carry: what this tenant
		// registered. Without it the model picks a plausible name and gets a
		// refusal it cannot learn from.
		"Registered names: ops_ticket, refund_request.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block does not mention %q\n---\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "file a ticket") {
		t.Errorf("the user's own message must come last, got:\n%s", got)
	}
}

// A kind an admin turned approval off for executes on proposal. Saying so is
// what stops the agent telling the user to go and approve something that has
// already run.
func TestActionsBlockSaysWhenAKindRunsWithoutApproval(t *testing.T) {
	cat := &stubActionCatalog{entries: []ActionCatalogEntry{
		{Kind: "send_message", Usage: "params: {…}", RequiresApproval: false},
	}}
	got := (&ChatRunner{}).WithActionCatalog(cat).withActionsContext(context.Background(), "q", "co-1")

	if !strings.Contains(got, "runs immediately on proposal") {
		t.Errorf("block should say the kind needs no approval:\n%s", got)
	}
}

// Three ways to have no catalog, one required outcome: the message the agent
// receives is the message it would have received before this block existed.
func TestNoCatalogLeavesTheTurnByteIdentical(t *testing.T) {
	const msg = "what were our total sales last month?"

	t.Run("never wired", func(t *testing.T) {
		if got := (&ChatRunner{}).withActionsContext(context.Background(), msg, "co-1"); got != msg {
			t.Errorf("got %q, want the message unchanged", got)
		}
	})

	t.Run("nothing enabled", func(t *testing.T) {
		r := (&ChatRunner{}).WithActionCatalog(&stubActionCatalog{})
		if got := r.withActionsContext(context.Background(), msg, "co-1"); got != msg {
			t.Errorf("got %q, want the message unchanged", got)
		}
	})

	t.Run("the read failed", func(t *testing.T) {
		cat := &stubActionCatalog{err: errors.New("control database is down")}
		r := (&ChatRunner{}).WithActionCatalog(cat)
		if got := r.withActionsContext(context.Background(), msg, "co-1"); got != msg {
			t.Errorf("got %q, want the message unchanged — a catalog is a hint, not a gate", got)
		}
		if cat.calls != 1 {
			t.Errorf("calls = %d, want 1", cat.calls)
		}
	})
}
