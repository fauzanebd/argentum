package app

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-N8's enqueue half: a person's message is counted on the conversation ledger
// before any of its turns is queued, and is never refused by it.

func ledgerOver(t *testing.T, addr string, c agentbudget.Ceilings) *agentbudget.Conversation {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	return agentbudget.NewConversation(rdb, c)
}

func askAfter(res *EnqueueResult, agent, question string) agentbudget.Ask {
	return agentbudget.Ask{CompanyID: "co-1", UserMsgID: res.UserMsgID, AgentID: agent, Question: question, Depth: 1}
}

// The ordering claim. Two agents addressed and a budget of three: if the fan-out
// were not on the ledger before the first ask, both asks below would be admitted.
func TestAMessagesFanOutIsOnTheLedgerBeforeAnyAsk(t *testing.T) {
	ctx := context.Background()
	conv := ledgerOver(t, miniredis.RunT(t).Addr(), agentbudget.Ceilings{MaxAgentTurns: 3})
	enq, q := fanOutFixture(t, threeAgentRoom())
	enq.WithConversationBudget(conv)

	res, err := enq.Enqueue(ctx, dashboardSend("@all thoughts on margin?"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(q.payloads) != 2 {
		t.Fatalf("enqueued %d turns, want 2", len(q.payloads))
	}

	if v, err := conv.Admit(ctx, askAfter(res, "ag-fin", "what is the stock figure for SKU 4471?")); err != nil || !v.Admitted {
		t.Fatalf("first ask = %+v, %v; want admitted as the third turn", v, err)
	}
	v, _ := conv.Admit(ctx, askAfter(res, "ag-ops", "and for SKU 4472?"))
	if v.Admitted || v.Dimension != agentbudget.DimensionTurns || v.Turns != 3 {
		t.Errorf("second ask = %+v, want refused on turns at 3 — the fan-out's two and the first ask", v)
	}

	// What each agent is recorded as asked is the message it received — the
	// cleaned one — so a colleague re-asking it is a repeat rather than a turn.
	if v, _ := conv.Admit(ctx, askAfter(res, "ag-ops", "thoughts on margin?")); !v.Repeat {
		t.Errorf("re-asking Ops the person's question = %+v, want a repeat", v)
	}
}

// A person who addresses more agents than the budget allows has still decided
// who their own message is for. Every agent they named gets it.
func TestAFanOutWiderThanTheBudgetStillReachesEveryoneAddressed(t *testing.T) {
	ctx := context.Background()
	conv := ledgerOver(t, miniredis.RunT(t).Addr(), agentbudget.Ceilings{MaxAgentTurns: 1})
	enq, q := fanOutFixture(t, threeAgentRoom())
	enq.WithConversationBudget(conv)

	res, err := enq.Enqueue(ctx, dashboardSend("@all thoughts?"))
	if err != nil {
		t.Fatalf("Enqueue = %v, want the person's message queued whole", err)
	}
	if len(q.payloads) != 2 {
		t.Errorf("enqueued %d turns, want 2 — one per agent addressed", len(q.payloads))
	}
	if v, _ := conv.Admit(ctx, askAfter(res, "ag-fin", "anything else?")); v.Admitted || v.Turns != 2 {
		t.Errorf("ask = %+v, want refused with both addressed turns counted", v)
	}
}

// "A tenant at zero credit still gets the credit refusal, not this one." The
// credit check runs first and the ledger is never touched.
func TestZeroCreditIsTheCreditRefusalAndTouchesNoLedger(t *testing.T) {
	mr := miniredis.RunT(t)
	enq, q := fanOutFixture(t, threeAgentRoom())
	enq.WithConversationBudget(ledgerOver(t, mr.Addr(), agentbudget.DefaultCeilings())).
		WithBudget(fakeBudget{verdict: BudgetExhausted})

	_, err := enq.Enqueue(context.Background(), dashboardSend("@all thoughts?"))

	if !errors.Is(err, domain.ErrInsufficientCredits) {
		t.Fatalf("Enqueue = %v, want ErrInsufficientCredits", err)
	}
	if len(q.payloads) != 0 {
		t.Errorf("enqueued %d turns for a tenant at zero", len(q.payloads))
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Errorf("a refused message wrote %v to Redis", keys)
	}
}

// The ledger failing open for a person: a Redis that cannot be reached leaves
// the message queued exactly as it would have been without a ledger.
func TestALedgerOutageDoesNotRefuseAPersonsMessage(t *testing.T) {
	dead := miniredis.NewMiniRedis()
	if err := dead.Start(); err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	addr := dead.Addr()
	dead.Close()
	enq, q := fanOutFixture(t, threeAgentRoom())
	enq.WithConversationBudget(ledgerOver(t, addr, agentbudget.DefaultCeilings()))

	if _, err := enq.Enqueue(context.Background(), dashboardSend("@all thoughts?")); err != nil {
		t.Fatalf("Enqueue = %v, want the message queued despite the ledger", err)
	}
	if len(q.payloads) != 2 {
		t.Errorf("enqueued %d turns, want 2", len(q.payloads))
	}
}
