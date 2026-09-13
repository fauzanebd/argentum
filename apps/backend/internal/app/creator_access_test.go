package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
)

// T-Z8, decision 11: "A watcher or a scheduled task runs as its creator,
// re-checked at fire time. A revoked grant disables the task and says why."
//
// The grant store is chat_enqueuer_access_test.go's, behind the real authz.

// creatorUsers is a user directory by id.
type creatorUsers map[string]*domain.User

func (u creatorUsers) GetByID(_ context.Context, id string) (*domain.User, error) {
	if x, ok := u[id]; ok {
		return x, nil
	}
	return nil, domain.ErrNotFound
}

// jobThreads is the dedicated threads jobs run in, by id.
type jobThreads map[string]*domain.ConversationThread

func (j jobThreads) GetByID(_ context.Context, id string) (*domain.ConversationThread, error) {
	if t, ok := j[id]; ok {
		return t, nil
	}
	return nil, domain.ErrNotFound
}

func activeUser(id, companyID string) *domain.User {
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return &domain.User{ID: id, CompanyID: companyID, ActivatedAt: &at}
}

// creatorFixture is a company whose default is HR, with two job threads: one
// unpinned (so it runs as HR) and one pinned to Finance. u-rina is active,
// u-yoga was removed, and u-other belongs to another company.
func creatorFixture() (*CreatorAccess, *agentGrants) {
	grants := newAgentGrants("ag-hr", "ag-fin")
	removed := activeUser("u-yoga", "co-1")
	gone := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	removed.DeactivatedAt = &gone
	users := creatorUsers{
		"u-rina":  activeUser("u-rina", "co-1"),
		"u-yoga":  removed,
		"u-other": activeUser("u-other", "co-2"),
	}
	threads := jobThreads{
		"th-default": {ID: "th-default", CompanyID: "co-1"},
		"th-fin":     {ID: "th-fin", CompanyID: "co-1", AgentID: "ag-fin"},
	}
	roster := &stubDefaultAgent{agent: &domain.Agent{ID: "ag-hr"}}
	return NewCreatorAccess(authz.New(grants), users, roster, threads), grants
}

func TestAJobIsCheckedAgainstTheAgentItRunsAsForItsCreator(t *testing.T) {
	cases := []struct {
		name       string
		restricted []string
		granted    []string
		creator    string
		thread     string
		want       domain.DisabledReason
	}{
		{name: "an open agent runs whoever created it — a removed creator included", creator: "u-yoga", thread: "th-default"},
		{name: "an open agent runs for a creator deleted outright", creator: "", thread: "th-default"},
		{name: "restricted, and the creator is granted it", restricted: []string{"ag-hr"}, granted: []string{"u-rina/ag-hr"},
			creator: "u-rina", thread: "th-default"},
		{name: "restricted, and the creator is not granted it", restricted: []string{"ag-hr"},
			creator: "u-rina", thread: "th-default", want: domain.DisabledReasonCreatorNotGranted},
		{name: "restricted, and the creator was removed though their grant row survived", restricted: []string{"ag-hr"},
			granted: []string{"u-yoga/ag-hr"}, creator: "u-yoga", thread: "th-default", want: domain.DisabledReasonCreatorRemoved},
		{name: "restricted, and the creator was deleted outright", restricted: []string{"ag-hr"},
			creator: "", thread: "th-default", want: domain.DisabledReasonCreatorRemoved},
		{name: "restricted, and the creator id is another company's person", restricted: []string{"ag-hr"},
			granted: []string{"u-other/ag-hr"}, creator: "u-other", thread: "th-default", want: domain.DisabledReasonCreatorRemoved},
		{name: "the thread's own agent is what is asked about, not the default", restricted: []string{"ag-hr"},
			creator: "u-rina", thread: "th-fin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ca, grants := creatorFixture()
			for _, id := range tc.restricted {
				grants.restricted[id] = true
			}
			for _, g := range tc.granted {
				grants.granted[g] = true
			}
			got, err := ca.Check(context.Background(), "co-1", tc.creator, tc.thread)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got != tc.want {
				t.Errorf("Check = %q, want %q", got, tc.want)
			}
		})
	}
}

// A check that cannot be made is an error, never a reason: a blip must neither
// switch a job off nor let it run unchecked.
func TestACreatorCheckThatCannotBeMadeIsAnErrorNotAReason(t *testing.T) {
	ca, grants := creatorFixture()
	grants.err = errors.New("control DB down")
	if got, err := ca.Check(context.Background(), "co-1", "u-rina", "th-default"); !errors.Is(err, ErrAccessCheckFailed) || got != "" {
		t.Errorf("Check = (%q, %v), want (\"\", ErrAccessCheckFailed)", got, err)
	}
	if got, err := (*CreatorAccess)(nil).Check(context.Background(), "co-1", "u-rina", "th-default"); err != nil || got != "" {
		t.Errorf("nil Check = (%q, %v), want nothing checked", got, err)
	}
}

// --- scheduled tasks ---------------------------------------------------

func scheduledFixture(creator string) (*ScheduledTaskService, *stubScheduledRepo, *agentGrants) {
	repo := &stubScheduledRepo{task: &domain.ScheduledTask{
		ID: "task-1", CompanyID: "co-1", UserID: creator, ThreadID: "th-default",
		Prompt: "kirim rekap gaji minggu ini", Enabled: true,
	}}
	ca, grants := creatorFixture()
	// A nil ThreadService, as scheduled_budget_test.go uses it: reaching the
	// prompt append means the gate let the tick through.
	return NewScheduledTaskService(repo, nil, nil, nil).WithCreatorAccess(ca), repo, grants
}

func TestAScheduleWhoseCreatorLostTheAgentIsSwitchedOffAndSaysWhy(t *testing.T) {
	svc, repo, grants := scheduledFixture("u-rina")
	grants.restricted["ag-hr"] = true

	if err := svc.HandleFire(context.Background(), "task-1"); err != nil {
		t.Fatalf("HandleFire = %v, want nil — the stop is recorded, not retried", err)
	}
	if len(repo.disabled) != 1 || repo.disabled[0] != domain.DisabledReasonCreatorNotGranted {
		t.Fatalf("disabled = %v, want switched off for creator_not_granted", repo.disabled)
	}
	if len(repo.updated) != 1 || repo.updated[0].Status != domain.ScheduledRunStatusFailed ||
		!strings.Contains(repo.updated[0].ErrorMessage, "not granted") {
		t.Errorf("runs closed = %+v, want one failed run whose message says why", repo.updated)
	}
}

// The acceptance's second line: "the case the previous line does not cover".
func TestAScheduleWhoseCreatorWasDeletedIsSwitchedOffToo(t *testing.T) {
	svc, repo, grants := scheduledFixture("") // user_id is SET NULL on delete
	grants.restricted["ag-hr"] = true

	if err := svc.HandleFire(context.Background(), "task-1"); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	if len(repo.disabled) != 1 || repo.disabled[0] != domain.DisabledReasonCreatorRemoved {
		t.Errorf("disabled = %v, want switched off for creator_removed", repo.disabled)
	}
}

func TestAScheduleCheckThatFailsRunsNothingAndSwitchesNothingOff(t *testing.T) {
	svc, repo, grants := scheduledFixture("u-rina")
	grants.err = errors.New("control DB down")

	if err := svc.HandleFire(context.Background(), "task-1"); err != nil {
		t.Fatalf("HandleFire = %v, want nil — the failed run is the record", err)
	}
	if len(repo.disabled) != 0 {
		t.Errorf("disabled = %v, want the task left on for the next tick to ask again", repo.disabled)
	}
	if len(repo.updated) != 1 || !strings.Contains(repo.updated[0].ErrorMessage, "try again") {
		t.Errorf("runs closed = %+v, want one failed run saying to try again", repo.updated)
	}
}

// Decision 3: a workspace with nothing restricted finds nothing changed — a
// schedule whose creator left keeps firing on an open agent.
func TestAScheduleOnAnOpenAgentFiresWhoeverCreatedIt(t *testing.T) {
	svc, repo, _ := scheduledFixture("")
	defer func() {
		_ = recover() // the nil ThreadService, reached only because the gate let the tick through
		if len(repo.disabled) != 0 || len(repo.updated) != 0 {
			t.Errorf("disabled %v, closed %d runs; want the tick to go through untouched", repo.disabled, len(repo.updated))
		}
	}()
	_ = svc.HandleFire(context.Background(), "task-1")
}

// --- watchers ----------------------------------------------------------

func TestAWatcherWhoseCreatorLostTheAgentIsSwitchedOffAtTheNextFire(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.ThreadID, w.CreatedBy = "th-default", "u-rina"
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		t.Error("the metric was evaluated for a watcher its creator may no longer run")
		return valueResult(500), nil
	}}
	enq := &fakeEnqueuer{}
	ca, grants := creatorFixture()
	grants.restricted["ag-hr"] = true
	s := newFireService(repo, me, enq, &fakeThreads{}, fakeBudget{verdict: BudgetOK}).WithCreatorAccess(ca)

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	got := repo.watchers[w.ID]
	if got.Enabled || got.DisabledReason != domain.DisabledReasonCreatorNotGranted {
		t.Errorf("watcher enabled=%v reason=%q, want switched off for creator_not_granted", got.Enabled, got.DisabledReason)
	}
	// "Not silently skipped": the tick that did it is in the events sheet.
	if ev := repo.lastEvent(); ev == nil || ev.SuppressedReason != string(domain.DisabledReasonCreatorNotGranted) {
		t.Errorf("last event = %+v, want one naming why it stopped", ev)
	}
	if enq.calls != 0 {
		t.Errorf("enqueued %d turns for a watcher that was switched off", enq.calls)
	}
}

func TestAWatcherCheckThatFailsIsRetriedNotSwitchedOff(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.ThreadID, w.CreatedBy = "th-default", "u-rina"
	repo.watchers[w.ID] = w
	ca, grants := creatorFixture()
	grants.err = errors.New("control DB down")
	s := newFireService(repo, &fakeMetricEval{def: testMetricDef()}, &fakeEnqueuer{}, &fakeThreads{}, fakeBudget{}).WithCreatorAccess(ca)

	if err := s.HandleFire(context.Background(), w.ID); !errors.Is(err, ErrAccessCheckFailed) {
		t.Fatalf("HandleFire = %v, want ErrAccessCheckFailed so asynq retries", err)
	}
	if !repo.watchers[w.ID].Enabled {
		t.Error("a watcher was switched off because its check could not be made")
	}
}
