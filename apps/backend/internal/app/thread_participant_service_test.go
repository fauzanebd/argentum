package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-N2's rules. Every one of them is a refusal, which is why they are stated
// here rather than inferred from a working room: the happy path is one INSERT,
// and the interesting behaviour is what the service will not do.

type fakeParticipants struct {
	rows    map[string][]*domain.ThreadParticipant // by thread id
	addErr  error
	added   []*domain.ThreadParticipant
	removed []string
}

func newFakeParticipants() *fakeParticipants {
	return &fakeParticipants{rows: map[string][]*domain.ThreadParticipant{}}
}

func (f *fakeParticipants) Add(_ context.Context, _ string, p *domain.ThreadParticipant) error {
	if f.addErr != nil {
		return f.addErr
	}
	for _, e := range f.rows[p.ThreadID] {
		if e.AgentID == p.AgentID {
			return domain.ErrAlreadyExists
		}
	}
	p.ID = "tp-" + p.AgentID
	p.AddedAt = time.Now()
	f.rows[p.ThreadID] = append(f.rows[p.ThreadID], p)
	f.added = append(f.added, p)
	return nil
}

func (f *fakeParticipants) ListByThread(_ context.Context, _, threadID string) ([]*domain.ThreadParticipant, error) {
	return f.rows[threadID], nil
}

func (f *fakeParticipants) Remove(_ context.Context, _, threadID, agentID string) error {
	for i, e := range f.rows[threadID] {
		if e.AgentID == agentID {
			f.rows[threadID] = append(f.rows[threadID][:i], f.rows[threadID][i+1:]...)
			f.removed = append(f.removed, agentID)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeParticipants) CountByThread(_ context.Context, _, threadID string) (int, error) {
	return len(f.rows[threadID]), nil
}

// oneThreadRepo serves a single thread by (company, id) and nothing else.
type oneThreadRepo struct {
	*fakeThreadRepo
	thread *domain.ConversationThread
}

func (o oneThreadRepo) GetForCompany(_ context.Context, companyID, id string) (*domain.ConversationThread, error) {
	if o.thread == nil || o.thread.ID != id || o.thread.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return o.thread, nil
}

func participantFixture(t *testing.T, defaultSpeaker string) (*ThreadParticipantService, *fakeParticipants) {
	t.Helper()
	fin := agentRow("ag-fin", "Finance")
	ops := agentRow("ag-ops", "Ops")
	hr := agentRow("ag-hr", "People")
	off := agentRow("ag-off", "Retired")
	off.Enabled = false
	roster := &fakeRoster{
		byID: map[string]*domain.Agent{"ag-fin": fin, "ag-ops": ops, "ag-hr": hr, "ag-off": off},
		def:  fin,
	}
	repo := newFakeParticipants()
	threads := oneThreadRepo{
		fakeThreadRepo: &fakeThreadRepo{},
		thread: &domain.ConversationThread{
			ID: "th-1", CompanyID: "co-1", AgentID: defaultSpeaker, CreatedAt: time.Now(),
		},
	}
	return NewThreadParticipantService(repo, threads, roster, 0), repo
}

func TestTheDefaultSpeakerIsInTheRoomWithoutARow(t *testing.T) {
	svc, repo := participantFixture(t, "ag-fin")

	got, err := svc.List(context.Background(), "co-1", "th-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].AgentID != "ag-fin" {
		t.Fatalf("room = %+v, want one implicit member ag-fin", got)
	}
	if got[0].AgentName != "Finance" {
		t.Errorf("implicit member name = %q, want Finance", got[0].AgentName)
	}
	if n := len(repo.rows["th-1"]); n != 0 {
		t.Errorf("%d participant rows written; the default speaker must need none", n)
	}
}

func TestAddedAgentsFollowTheDefaultSpeaker(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")
	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-ops", "u-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, _ := svc.List(context.Background(), "co-1", "th-1")
	if len(got) != 2 || got[0].AgentID != "ag-fin" || got[1].AgentID != "ag-ops" {
		t.Fatalf("room = %+v, want [ag-fin ag-ops]", got)
	}
}

// A thread with no agent runs as the company default, resolved per turn.
// Naming one here would pin the conversation to whichever agent happens to be
// default today, which is a different fact.
func TestAThreadWithNoDefaultSpeakerHasNoImplicitMember(t *testing.T) {
	svc, _ := participantFixture(t, "")

	got, err := svc.List(context.Background(), "co-1", "th-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("room = %+v, want empty", got)
	}
}

func TestAnotherCompanysThreadIsNotFound(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")

	_, err := svc.List(context.Background(), "co-2", "th-1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("List for another company = %v, want ErrNotFound", err)
	}
}

// 404 and not 403: the caller is a browser holding a bare uuid, and a
// distinguishable error is an existence oracle across tenants.
func TestAnotherCompanysAgentCannotJoin(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")

	_, err := svc.Add(context.Background(), "co-1", "th-1", "ag-elsewhere", "u-1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Add of an unknown agent = %v, want ErrNotFound", err)
	}
}

func TestADisabledAgentCannotJoin(t *testing.T) {
	svc, repo := participantFixture(t, "ag-fin")

	_, err := svc.Add(context.Background(), "co-1", "th-1", "ag-off", "u-1")
	if !errors.Is(err, ErrAgentDisabled) {
		t.Errorf("Add of a disabled agent = %v, want ErrAgentDisabled", err)
	}
	if len(repo.added) != 0 {
		t.Error("a disabled agent was written to the room")
	}
}

// The default speaker is already in the room and has no row, so the unique
// index cannot fire. Without this check the room lists the same agent twice.
func TestTheDefaultSpeakerCannotBeAddedAgain(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")

	_, err := svc.Add(context.Background(), "co-1", "th-1", "ag-fin", "u-1")
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("re-adding the default speaker = %v, want ErrAlreadyExists", err)
	}
	got, _ := svc.List(context.Background(), "co-1", "th-1")
	if len(got) != 1 {
		t.Errorf("room = %+v, want one member", got)
	}
}

func TestTheDefaultSpeakerCannotBeRemoved(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")

	err := svc.Remove(context.Background(), "co-1", "th-1", "ag-fin")
	if !errors.Is(err, ErrDefaultSpeaker) {
		t.Errorf("removing the default speaker = %v, want ErrDefaultSpeaker", err)
	}
}

func TestAnAddedAgentCanBeRemoved(t *testing.T) {
	svc, repo := participantFixture(t, "ag-fin")
	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-ops", "u-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := svc.Remove(context.Background(), "co-1", "th-1", "ag-ops"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(repo.rows["th-1"]) != 0 {
		t.Error("the agent is still in the room")
	}
}

// The cap counts the implicit default speaker. A ceiling that counted only
// rows would let a four-agent room hold five.
func TestTheCapCountsTheDefaultSpeaker(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")
	svc.max = 2

	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-ops", "u-1"); err != nil {
		t.Fatalf("second member refused: %v", err)
	}
	_, err := svc.Add(context.Background(), "co-1", "th-1", "ag-hr", "u-1")
	if !errors.Is(err, ErrParticipantLimit) {
		t.Errorf("third member into a room of 2 = %v, want ErrParticipantLimit", err)
	}
}

func TestTheLimitRefusalNamesTheLimit(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")
	svc.max = 1

	_, err := svc.Add(context.Background(), "co-1", "th-1", "ag-ops", "u-1")
	if !errors.Is(err, ErrParticipantLimit) {
		t.Fatalf("Add = %v, want ErrParticipantLimit", err)
	}
	if !contains(err.Error(), "(1)") {
		t.Errorf("refusal %q does not name the limit", err)
	}
}

// The default is domain.MaxThreadParticipants, applied by the constructor, so
// an unset THREAD_MAX_PARTICIPANTS does not mean "no agents allowed".
func TestAnUnsetCapTakesTheDomainDefault(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")
	if svc.max != domain.MaxThreadParticipants {
		t.Errorf("max = %d, want %d", svc.max, domain.MaxThreadParticipants)
	}
}

// T-N10: "a widget session cannot create or address a multi-agent thread". The
// widget has no participant route, so the door that could make one is a member
// on the dashboard holding a widget conversation's id — company-scoped, so the
// tenant check lets it through. Nothing may be written, and the refusal must
// come before the roster read so it says nothing about the agent.
func TestAWidgetConversationCannotBecomeARoom(t *testing.T) {
	roster := &fakeRoster{byID: map[string]*domain.Agent{"ag-ops": agentRow("ag-ops", "Ops")}}
	repo := newFakeParticipants()
	threads := oneThreadRepo{
		fakeThreadRepo: &fakeThreadRepo{},
		thread: &domain.ConversationThread{
			ID: "th-w", CompanyID: "co-1", AgentID: "ag-fin", Channel: domain.ChannelWidget,
			EmbedUserRef: "visitor-9", CreatedAt: time.Now(),
		},
	}
	svc := NewThreadParticipantService(repo, threads, roster, 0)

	_, err := svc.Add(context.Background(), "co-1", "th-w", "ag-ops", "u-1")

	if !errors.Is(err, ErrRoomNotOnWidget) {
		t.Fatalf("Add = %v, want ErrRoomNotOnWidget", err)
	}
	if n, _ := repo.CountByThread(context.Background(), "co-1", "th-w"); n != 0 {
		t.Errorf("%d participant row(s) written into a widget conversation", n)
	}
}

// The ceiling POST /v1/threads checks a room against before opening it is the
// one Add enforces, not a second number.
func TestCapacityIsTheCeilingAddEnforces(t *testing.T) {
	svc, _ := participantFixture(t, "ag-fin")
	if got := svc.Capacity(); got != domain.MaxThreadParticipants {
		t.Errorf("Capacity() = %d, want the domain default %d", got, domain.MaxThreadParticipants)
	}
	pinned := NewThreadParticipantService(newFakeParticipants(), oneThreadRepo{fakeThreadRepo: &fakeThreadRepo{}}, &fakeRoster{}, 2)
	if got := pinned.Capacity(); got != 2 {
		t.Errorf("Capacity() = %d, want the configured 2", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
