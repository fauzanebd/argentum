package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// `agent_id` on `/v1` (T-S5). The pick itself is T-S3's and is tested beside
// it; what is here is the half only the API has — a caller who names an agent
// without naming a conversation, because the API resolves threads by the
// tenant's own `user_ref` rather than by a thread the user clicked on.

func TestANewAPIThreadIsPinnedToThePickedAgent(t *testing.T) {
	repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
	svc := newThreadService(repo, &fakeClassifierLLM{})

	res, err := svc.ResolveForAPIUser(context.Background(), "co-1", "their-user-42", "revenue?", "ag-fin")
	if err != nil {
		t.Fatalf("ResolveForAPIUser: %v", err)
	}
	if res.Thread.AgentID != "ag-fin" {
		t.Errorf("agent_id = %q, want the picked agent on the row", res.Thread.AgentID)
	}
}

// Empty stays empty rather than being resolved to the default and frozen in.
// An unpinned conversation asks agentFor per turn, which is what lets a company
// move its default and move every unpinned conversation with it.
func TestAnAPIThreadWithNoPickIsStoredUnpinned(t *testing.T) {
	repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
	svc := newThreadService(repo, &fakeClassifierLLM{})

	res, err := svc.ResolveForAPIUser(context.Background(), "co-1", "their-user-42", "revenue?", "")
	if err != nil {
		t.Fatalf("ResolveForAPIUser: %v", err)
	}
	if res.Thread.AgentID != "" {
		t.Errorf("agent_id = %q, want empty", res.Thread.AgentID)
	}
}

// The `user_ref` door has no thread id to disagree with, so a pick that
// disagrees with the conversation the resolver picked forks rather than
// refusing. Refusing would break the caller who sends `agent_id` on every
// request the moment their first conversation exists.
func TestNamingADifferentAgentForksTheConversation(t *testing.T) {
	warm := &domain.ConversationThread{
		ID: "th-ops", CompanyID: "co-1", Channel: domain.ChannelAPI,
		APIUserRef: "their-user-42", AgentID: "ag-ops", LastMessageAt: time.Now(),
	}
	repo := &fakeThreadRepo{latest: warm}
	enq := &ChatEnqueuer{threads: newThreadService(repo, &fakeClassifierLLM{})}

	got, err := enq.forkForAgent(context.Background(),
		ChatInput{CompanyID: "co-1", APIUserRef: "their-user-42", Message: "revenue?"},
		&ResolveResult{Thread: warm, IsNew: false}, "ag-fin")
	if err != nil {
		t.Fatalf("forkForAgent: %v", err)
	}
	if !got.IsNew || got.Thread.ID == warm.ID {
		t.Fatalf("result = %+v, want a new conversation", got.Thread)
	}
	if got.Thread.AgentID != "ag-fin" {
		t.Errorf("agent_id = %q, want the fork pinned to the agent that caused it", got.Thread.AgentID)
	}
	if got.Thread.APIUserRef != "their-user-42" {
		t.Errorf("api_user_ref = %q, want the fork keyed to the same end user", got.Thread.APIUserRef)
	}
}

// Agreement is not a fork. Two shapes of it: the conversation already names the
// agent, and the conversation names none while the agent named *is* the company
// default — which is the same thing said two ways, and comparing against the
// stored column alone would fork on every turn for the second.
func TestNamingTheAgentAConversationAlreadyRunsAsDoesNotFork(t *testing.T) {
	cases := map[string]struct {
		stored, pinned string
	}{
		"the conversation names it":            {stored: "ag-fin", pinned: "ag-fin"},
		"the conversation runs as the default": {stored: "", pinned: "ag-def"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			warm := &domain.ConversationThread{
				ID: "th-1", CompanyID: "co-1", Channel: domain.ChannelAPI,
				APIUserRef: "their-user-42", AgentID: tc.stored, LastMessageAt: time.Now(),
			}
			repo := &fakeThreadRepo{latest: warm}
			enq := (&ChatEnqueuer{threads: newThreadService(repo, &fakeClassifierLLM{})}).
				WithRoster(&stubDefaultAgent{agent: &domain.Agent{ID: "ag-def"}})

			got, err := enq.forkForAgent(context.Background(),
				ChatInput{CompanyID: "co-1", APIUserRef: "their-user-42", Message: "and December?"},
				&ResolveResult{Thread: warm, IsNew: false}, tc.pinned)
			if err != nil {
				t.Fatalf("forkForAgent: %v", err)
			}
			if got.IsNew || got.Thread.ID != "th-1" {
				t.Errorf("result = %+v, want the conversation continued", got.Thread)
			}
			if len(repo.created) != 0 {
				t.Errorf("created %d thread(s) for a pick that changed nothing", len(repo.created))
			}
		})
	}
}

// A brand-new conversation is already the picked agent's. Nothing to fork away
// from, and forking would double every first turn.
func TestAFreshConversationIsNeverForked(t *testing.T) {
	repo := &fakeThreadRepo{}
	enq := &ChatEnqueuer{threads: newThreadService(repo, &fakeClassifierLLM{})}
	fresh := &ResolveResult{Thread: &domain.ConversationThread{ID: "th-new", AgentID: "ag-fin"}, IsNew: true}

	got, err := enq.forkForAgent(context.Background(),
		ChatInput{CompanyID: "co-1", APIUserRef: "u"}, fresh, "ag-fin")
	if err != nil {
		t.Fatalf("forkForAgent: %v", err)
	}
	if got != fresh || len(repo.created) != 0 {
		t.Errorf("result = %+v, want the thread that was just created", got)
	}
}

// The acceptance item in as many words: another company's agent id starts no
// turn. The enqueuer is built with a nil thread service, and that is the
// assertion rather than laziness — if the pick ever stops running before the
// resolver, the next line dereferences nil and this fails loudly instead of
// quietly creating a thread and a user message for a request that must not
// have produced either.
func TestAnAgentThisCompanyCannotUseStartsNothing(t *testing.T) {
	enq := NewChatEnqueuer(nil, nil, nil, nil).WithRoster(
		rosterWith(&domain.Agent{ID: "ag-theirs", CompanyID: "co-2", Enabled: true}))

	_, err := enq.Enqueue(context.Background(), ChatInput{
		Channel:    domain.ChannelAPI,
		CompanyID:  "co-1",
		APIUserRef: "their-user-42",
		AgentID:    "ag-theirs",
		Message:    "what is in the HR database?",
	})

	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("Enqueue error = %v, want ErrAgentNotFound", err)
	}
	// And it is still a 404 to anything that only knows about domain
	// sentinels, so the dashboard's own mapping is unaffected.
	if !errors.Is(err, domain.ErrNotFound) {
		t.Error("ErrAgentNotFound no longer wraps domain.ErrNotFound")
	}
}

// Naming both a conversation and an agent that conversation does not run as is
// refused, not silently ignored — on `/v1` for the same reason as on the
// dashboard, and before the user message is appended so a refused call leaves
// no trace in a transcript.
func TestChangingAgentOnAnExistingAPIThreadIsRefused(t *testing.T) {
	warm := &domain.ConversationThread{
		ID: "th-ops", CompanyID: "co-1", Channel: domain.ChannelAPI,
		APIUserRef: "their-user-42", AgentID: "ag-ops", LastMessageAt: time.Now(),
	}
	repo := &fakeThreadRepo{byID: map[string]*domain.ConversationThread{"th-ops": warm}}
	roster := rosterWith(&domain.Agent{ID: "ag-fin", CompanyID: "co-1", Enabled: true})
	enq := (&ChatEnqueuer{threads: newThreadService(repo, &fakeClassifierLLM{})}).WithRoster(roster)

	_, err := enq.Enqueue(context.Background(), ChatInput{
		Channel:   domain.ChannelAPI,
		CompanyID: "co-1",
		ThreadID:  "th-ops",
		AgentID:   "ag-fin",
		Message:   "and December?",
	})

	if !errors.Is(err, ErrAgentChange) {
		t.Fatalf("Enqueue error = %v, want ErrAgentChange", err)
	}
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Error("ErrAgentChange no longer wraps domain.ErrInvalidInput")
	}
}

// refusingMessages is a message repository that fails on write.
//
// It is what lets the test below prove a *negative* — that agreement is not
// refused — without standing up a company repository and a live queue. A pick
// that agreed gets past the check and reaches the append; the refusal it is
// tested against returns two steps earlier and never touches this.
type refusingMessages struct{}

func (refusingMessages) Append(context.Context, *domain.Message) error {
	return errors.New("the message store is not wired in this test")
}
func (refusingMessages) ListByThread(context.Context, string, int, int) ([]*domain.Message, error) {
	panic("unexpected ListByThread")
}
func (refusingMessages) ListPageByThread(context.Context, string, domain.MessageFilter) ([]*domain.Message, bool, error) {
	panic("unexpected ListPageByThread")
}
func (refusingMessages) LatestByThread(context.Context, string) (*domain.Message, error) {
	panic("unexpected LatestByThread")
}
func (refusingMessages) LatestAssistantSince(context.Context, string, time.Time) (*domain.Message, error) {
	panic("unexpected LatestAssistantSince")
}
func (refusingMessages) CountByThread(context.Context, string) (int, error) {
	panic("unexpected CountByThread")
}

// Naming the agent a conversation already runs as is agreement, and agreement
// must get through. Without this the refusal above could be tightened into "any
// `agent_id` on a `thread_id` is refused" and nothing would notice — which
// would make the field unusable for the caller who sends it on every request.
func TestNamingTheSameAgentOnAnExistingAPIThreadIsAllowed(t *testing.T) {
	warm := &domain.ConversationThread{
		ID: "th-fin", CompanyID: "co-1", Channel: domain.ChannelAPI,
		APIUserRef: "their-user-42", AgentID: "ag-fin", LastMessageAt: time.Now(),
	}
	repo := &fakeThreadRepo{byID: map[string]*domain.ConversationThread{"th-fin": warm}}
	roster := rosterWith(&domain.Agent{ID: "ag-fin", CompanyID: "co-1", Enabled: true})
	svc := NewThreadService(repo, refusingMessages{}, NewTopicClassifier(&fakeClassifierLLM{}),
		&fakeClassifierLLM{}, ThreadServiceConfig{IdleMinutes: 30, DashboardIdleHours: 4, SummaryEveryNTurns: 8})
	enq := (&ChatEnqueuer{threads: svc}).WithRoster(roster)

	_, err := enq.Enqueue(context.Background(), ChatInput{
		Channel:   domain.ChannelAPI,
		CompanyID: "co-1",
		ThreadID:  "th-fin",
		AgentID:   "ag-fin",
		Message:   "and December?",
	})

	if errors.Is(err, ErrAgentChange) {
		t.Fatalf("Enqueue refused a pick that agreed with the conversation: %v", err)
	}
	// It got as far as writing the question, which is two steps past the check.
	if err == nil || !strings.Contains(err.Error(), "append user message") {
		t.Fatalf("err = %v, want the append failure this fixture is built to produce", err)
	}
}
