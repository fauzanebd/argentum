package app

import (
	"context"
	"errors"
	"strings"
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

	// byID is the roster GetByID answers from, keyed by "companyID/agentID" so
	// a lookup with the right id and the wrong company misses — which is how
	// the repository behaves and the only reason the cross-tenant 404 works.
	byID map[string]*domain.Agent
	// byIDErr is a transport-level failure, distinct from an id that is simply
	// not there.
	byIDErr error
}

func (s *stubDefaultAgent) GetDefault(context.Context, string) (*domain.Agent, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.agent, nil
}

func (s *stubDefaultAgent) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	if s.byIDErr != nil {
		return nil, s.byIDErr
	}
	if a, ok := s.byID[companyID+"/"+id]; ok {
		return a, nil
	}
	return nil, domain.ErrNotFound
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

// The pick half of T-S3: what a caller is allowed to name. agentFor above
// decides what a turn *runs* as; this decides what may be written onto a thread
// in the first place, which is the only place the roster is an authorization
// question rather than a configuration one.

func rosterWith(agents ...*domain.Agent) *stubDefaultAgent {
	byID := map[string]*domain.Agent{}
	for _, a := range agents {
		byID[a.CompanyID+"/"+a.ID] = a
	}
	return &stubDefaultAgent{byID: byID}
}

func TestPickingAnAgentTheCompanyOwns(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoster(
		rosterWith(&domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true}))

	got, err := enq.pickAgent(context.Background(), "co-1", "ag-ops")
	if err != nil {
		t.Fatalf("pickAgent: %v", err)
	}
	if got != "ag-ops" {
		t.Errorf("pickAgent = %q, want ag-ops", got)
	}
}

// Empty stays empty rather than resolving to the default here. The thread is
// stored unpinned so agentFor answers per turn, which is what lets a company
// move its default and move every unpinned conversation with it.
func TestNoPickLeavesTheThreadUnpinned(t *testing.T) {
	roster := rosterWith(&domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true})
	roster.agent = &domain.Agent{ID: "ag-def"}
	enq := (&ChatEnqueuer{}).WithRoster(roster)

	got, err := enq.pickAgent(context.Background(), "co-1", "")
	if err != nil {
		t.Fatalf("pickAgent: %v", err)
	}
	if got != "" {
		t.Errorf("pickAgent = %q, want empty so the default resolves per turn", got)
	}
	if roster.calls != 0 {
		t.Error("the default was resolved at pick time; it must be resolved per turn")
	}
}

// The three refusals are one error on purpose. Unknown, another company's, and
// disabled are distinguishable to us and must not be to a browser holding a
// bare uuid: two of them would confirm a row exists.
func TestEveryRefusedPickIsTheSameNotFound(t *testing.T) {
	roster := rosterWith(
		&domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true},
		&domain.Agent{ID: "ag-hr", CompanyID: "co-1", Enabled: false},
		&domain.Agent{ID: "ag-other", CompanyID: "co-2", Enabled: true},
	)
	enq := (&ChatEnqueuer{}).WithRoster(roster)

	cases := map[string]string{
		"an id that never existed": "ag-nope",
		"another company's agent":  "ag-other",
		"a disabled agent":         "ag-hr",
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := enq.pickAgent(context.Background(), "co-1", id)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("pickAgent error = %v, want ErrNotFound", err)
			}
			if got != "" {
				t.Errorf("pickAgent = %q, want empty on refusal", got)
			}
			if msg := err.Error(); !strings.Contains(msg, "no such agent") {
				t.Errorf("error text = %q; every refusal must read the same", msg)
			}
		})
	}
}

// A roster that cannot be read is not a refusal. It is a 500's worth of
// trouble, and telling the caller "no such agent" would send an admin looking
// for a row that is right there.
func TestAnUnreadableRosterIsNotANotFound(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoster(&stubDefaultAgent{byIDErr: errors.New("control DB down")})

	if _, err := enq.pickAgent(context.Background(), "co-1", "ag-ops"); errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a failed lookup was reported as ErrNotFound: %v", err)
	} else if err == nil {
		t.Error("a failed lookup was swallowed")
	}
}

// A deployment with no roster wired must not refuse a pick, or a dashboard
// offering a picker against a stripped-down build breaks every new chat.
func TestNoRosterWiredDropsThePickRatherThanRefusingIt(t *testing.T) {
	got, err := (&ChatEnqueuer{}).pickAgent(context.Background(), "co-1", "ag-ops")
	if err != nil {
		t.Fatalf("pickAgent: %v", err)
	}
	if got != "" {
		t.Errorf("pickAgent = %q, want empty", got)
	}
}
