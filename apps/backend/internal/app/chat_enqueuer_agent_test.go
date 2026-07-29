package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The enqueue half of T-S2: which agent gets pinned onto the payload. The
// worker can re-resolve, so nothing here is load-bearing for correctness —
// what it is load-bearing for is that the two processes cannot disagree about
// which agent a turn ran as, and T-S4 sets this same field from a channel
// binding.

type stubDefaultAgent struct {
	agent *domain.Agent
	err   error
	calls int
}

func (s *stubDefaultAgent) GetDefault(context.Context, string) (*domain.Agent, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.agent, nil
}

func TestTheThreadsOwnAgentWins(t *testing.T) {
	roster := &stubDefaultAgent{agent: &domain.Agent{ID: "ag-def"}}
	enq := (&ChatEnqueuer{}).WithRoster(roster)

	got := enq.agentFor(context.Background(),
		&domain.ConversationThread{CompanyID: "co-1", AgentID: "ag-ops"})

	if got != "ag-ops" {
		t.Errorf("agentFor = %q, want the thread's own agent", got)
	}
	if roster.calls != 0 {
		t.Error("the default was looked up for a thread that already names an agent")
	}
}

func TestAThreadWithNoAgentGetsTheCompanyDefault(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoster(&stubDefaultAgent{agent: &domain.Agent{ID: "ag-def"}})

	got := enq.agentFor(context.Background(), &domain.ConversationThread{CompanyID: "co-1"})

	if got != "ag-def" {
		t.Errorf("agentFor = %q, want the company default", got)
	}
}

// Every way of having no answer produces an empty field and a turn that still
// runs. The worker resolves the default itself, so the cost of being wrong
// here is one repeated lookup — and the cost of refusing would be a tenant who
// cannot ask a question.
func TestNoDefaultAgentStillEnqueuesTheTurn(t *testing.T) {
	cases := map[string]*ChatEnqueuer{
		"no roster wired":       {},
		"company has no roster": (&ChatEnqueuer{}).WithRoster(&stubDefaultAgent{err: domain.ErrNotFound}),
		"the roster cannot be read": (&ChatEnqueuer{}).WithRoster(
			&stubDefaultAgent{err: errors.New("control DB down")}),
	}
	for name, enq := range cases {
		t.Run(name, func(t *testing.T) {
			if got := enq.agentFor(context.Background(),
				&domain.ConversationThread{CompanyID: "co-1"}); got != "" {
				t.Errorf("agentFor = %q, want empty", got)
			}
		})
	}
}
