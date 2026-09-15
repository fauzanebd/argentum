package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z4: who may talk to which agent, at every point a dashboard turn picks one.
//
// The decision is internal/authz's, so these tests put the real Authorizer
// behind the enqueuer over a fake grant store — what is proven is the rule the
// product runs, not a second copy of it. The role is not an input anywhere on
// this path (ChatInput carries none, and authz would not read it), which is
// decision 4 held by construction; the role-by-role table lives in authz.

// agentGrants is a grant store for company co-1's agents. It counts loads,
// because "the ordinary turn is one read" and "a door with no person never
// asks" are both assertions about that count.
type agentGrants struct {
	known      map[string]bool
	restricted map[string]bool
	granted    map[string]bool // "user/agent"
	err        error
	loads      int
}

func newAgentGrants(ids ...string) *agentGrants {
	g := &agentGrants{known: map[string]bool{}, restricted: map[string]bool{}, granted: map[string]bool{}}
	for _, id := range ids {
		g.known[id] = true
	}
	return g
}

func (g *agentGrants) LoadAccess(_ context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	g.loads++
	if g.err != nil {
		return nil, g.err
	}
	out := map[string]domain.ResourceAccess{}
	if companyID != "co-1" || kind != domain.ResourceKindAgent {
		return out, nil
	}
	for _, id := range ids {
		if !g.known[id] {
			continue
		}
		mode := domain.AccessModeOpen
		if g.restricted[id] {
			mode = domain.AccessModeRestricted
		}
		out[id] = domain.ResourceAccess{Mode: mode, Granted: g.granted[userID+"/"+id]}
	}
	return out, nil
}

// rosterList is AgentLister over a fixed, ordered roster.
type rosterList []*domain.Agent

func (r rosterList) ListByCompany(_ context.Context, companyID string) ([]*domain.Agent, error) {
	var out []*domain.Agent
	for _, a := range r {
		if a.CompanyID == companyID {
			out = append(out, a)
		}
	}
	return out, nil
}

// appendCounter is stubMessages that counts appends, so "a refused turn
// leaves no orphan user message" is checked rather than assumed.
type appendCounter struct {
	stubMessages
	appended int
	// last is the most recent message appended, for the one test that reads
	// what was written rather than whether anything was (T-W9).
	last *domain.Message
}

func (m *appendCounter) Append(ctx context.Context, msg *domain.Message) error {
	m.appended++
	m.last = msg
	return m.stubMessages.Append(ctx, msg)
}

type accessFixture struct {
	enq     *ChatEnqueuer
	queue   *recordingEnqueuer
	threads *fakeThreadRepo
	msgs    *appendCounter
	grants  *agentGrants
	roster  *stubDefaultAgent
	agents  map[string]*domain.Agent
}

// newAccessFixture is a company with four agents — HR, Finance, Ops, Legal —
// listed default first as the repository lists them, every one open until a
// test restricts it, and the person asking is u-1.
func newAccessFixture(t *testing.T, defaultID string, room *stubRoom, existing ...*domain.ConversationThread) *accessFixture {
	t.Helper()
	all := []*domain.Agent{
		{ID: "ag-hr", CompanyID: "co-1", Name: "HR", Enabled: true},
		{ID: "ag-fin", CompanyID: "co-1", Name: "Finance", Enabled: true},
		{ID: "ag-ops", CompanyID: "co-1", Name: "Ops", Enabled: true},
		{ID: "ag-legal", CompanyID: "co-1", Name: "Legal", Enabled: true},
	}
	byName := map[string]*domain.Agent{}
	var ordered rosterList
	for _, a := range all {
		byName[a.ID] = a
		if a.ID == defaultID {
			a.IsDefault = true
			ordered = append(rosterList{a}, ordered...)
		} else {
			ordered = append(ordered, a)
		}
	}
	roster := rosterWith(all...)
	roster.agent = byName[defaultID]

	byID := map[string]*domain.ConversationThread{}
	for _, th := range existing {
		byID[th.ID] = th
	}
	repo := &fakeThreadRepo{byID: byID, latestErr: domain.ErrNotFound}
	msgs := &appendCounter{}
	svc := NewThreadService(quietThreadRepo{repo}, msgs, nil, nil, ThreadServiceConfig{
		IdleMinutes: 30, SummaryEveryNTurns: 8,
	})
	grants := newAgentGrants("ag-hr", "ag-fin", "ag-ops", "ag-legal")
	q := &recordingEnqueuer{}
	enq := NewChatEnqueuer(svc, msgs, fakeCompanies{}, q).
		WithRoster(roster).
		WithAgentAccess(authz.New(grants), ordered)
	if room != nil {
		enq = enq.WithRoom(room)
	}
	return &accessFixture{enq: enq, queue: q, threads: repo, msgs: msgs, grants: grants, roster: roster, agents: byName}
}

func newChat(msg, agentID string) ChatInput {
	return ChatInput{Channel: domain.ChannelDashboard, CompanyID: "co-1", UserID: "u-1", Message: msg, AgentID: agentID}
}

func onThread(threadID, msg string) ChatInput {
	in := newChat(msg, "")
	in.ThreadID = threadID
	return in
}

func dashboardThread(id, agentID string) *domain.ConversationThread {
	return &domain.ConversationThread{ID: id, CompanyID: "co-1", Channel: domain.ChannelDashboard, AgentID: agentID}
}

// assertNothingWritten is the ordering every refusal on this path keeps: no
// thread, no user message, no turn.
func (f *accessFixture) assertNothingWritten(t *testing.T) {
	t.Helper()
	if n := len(f.threads.created); n != 0 {
		t.Errorf("a refused turn created %d thread(s)", n)
	}
	if f.msgs.appended != 0 {
		t.Errorf("a refused turn appended %d user message(s)", f.msgs.appended)
	}
	if n := len(f.queue.payloads); n != 0 {
		t.Errorf("a refused turn enqueued %d run(s)", n)
	}
}

// Seam one: the pick. A restricted agent named by id — from a stale tab, or
// typed — is refused exactly as an agent that does not exist, because the
// picker never offered it. Both doors that pick: the send that opens a
// conversation, and the "New conversation" button.
func TestAPickOfARestrictedAgentIsRefusedAsNotFound(t *testing.T) {
	f := newAccessFixture(t, "ag-fin", nil)
	f.grants.restricted["ag-hr"] = true
	// Somebody else's grant opens it for them and for nobody else.
	f.grants.granted["u-2/ag-hr"] = true

	if _, err := f.enq.Enqueue(context.Background(), newChat("what is payroll?", "ag-hr")); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("Enqueue on a restricted pick: %v, want ErrAgentNotFound", err)
	}
	if _, err := f.enq.CreateDashboardThread(context.Background(), "co-1", "u-1", "ag-hr"); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("CreateDashboardThread on a restricted pick: %v, want ErrAgentNotFound", err)
	}
	f.assertNothingWritten(t)

	f.grants.granted["u-1/ag-hr"] = true
	res, err := f.enq.Enqueue(context.Background(), newChat("what is payroll?", "ag-hr"))
	if err != nil {
		t.Fatalf("Enqueue once granted: %v", err)
	}
	if res.Thread.AgentID != "ag-hr" || f.queue.payloads[0].AgentID != "ag-hr" {
		t.Errorf("granted pick ran as thread=%q payload=%q, want ag-hr", res.Thread.AgentID, f.queue.payloads[0].AgentID)
	}
}

// Seam two: default resolution. "A restricted agent's default is not a
// default."
func TestARestrictedDefaultIsNotThePersonsDefault(t *testing.T) {
	t.Run("an open default leaves the conversation unpinned, exactly as before", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		res, err := f.enq.Enqueue(context.Background(), newChat("hello", ""))
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if res.Thread.AgentID != "" {
			t.Errorf("thread pinned to %q; an open default must stay resolved per turn", res.Thread.AgentID)
		}
		if got := f.queue.payloads[0].AgentID; got != "ag-hr" {
			t.Errorf("turn ran as %q, want the default ag-hr", got)
		}
	})

	t.Run("a restricted default falls through to the first agent the person may use, pinned", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		f.grants.restricted["ag-hr"] = true
		res, err := f.enq.Enqueue(context.Background(), newChat("hello", ""))
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if res.Thread.AgentID != "ag-fin" || f.queue.payloads[0].AgentID != "ag-fin" {
			t.Fatalf("thread=%q payload=%q, want both ag-fin — the first open agent in roster order",
				res.Thread.AgentID, f.queue.payloads[0].AgentID)
		}
		// Pinned, so the conversation's second message is not refused for the
		// default it never ran as.
		f.threads.byID[res.Thread.ID] = res.Thread
		if _, err := f.enq.Enqueue(context.Background(), onThread(res.Thread.ID, "and again")); err != nil {
			t.Errorf("second message on the fallen-through conversation: %v", err)
		}
		if th, err := f.enq.CreateDashboardThread(context.Background(), "co-1", "u-1", ""); err != nil || th.AgentID != "ag-fin" {
			t.Errorf("CreateDashboardThread = (%v, %v), want a thread pinned to ag-fin", th, err)
		}
	})

	t.Run("a disabled agent is not fallen through to", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		f.grants.restricted["ag-hr"] = true
		f.agents["ag-fin"].Enabled = false
		res, err := f.enq.Enqueue(context.Background(), newChat("hello", ""))
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if res.Thread.AgentID != "ag-ops" {
			t.Errorf("fell through to %q, want ag-ops past the disabled Finance", res.Thread.AgentID)
		}
	})

	t.Run("nothing open to the person is a plain refusal, with nothing written", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		for id := range f.agents {
			f.grants.restricted[id] = true
		}
		if _, err := f.enq.Enqueue(context.Background(), newChat("hello", "")); !errors.Is(err, ErrNoAgentAvailable) {
			t.Fatalf("Enqueue: %v, want ErrNoAgentAvailable", err)
		}
		if _, err := f.enq.CreateDashboardThread(context.Background(), "co-1", "u-1", ""); !errors.Is(err, ErrNoAgentAvailable) {
			t.Fatalf("CreateDashboardThread: %v, want ErrNoAgentAvailable", err)
		}
		f.assertNothingWritten(t)
	})
}

// Seam three: thread rehydration. The acceptance line — "an existing thread
// whose agent later became restricted refuses on the next turn and says why".
// The transcript's routes are untouched by this ticket, which is what keeps it
// readable.
func TestAConversationWhoseAgentWasRestrictedRefusesAndSaysWhy(t *testing.T) {
	f := newAccessFixture(t, "ag-fin", nil, dashboardThread("th-hr", "ag-hr"))

	if _, err := f.enq.Enqueue(context.Background(), onThread("th-hr", "before")); err != nil {
		t.Fatalf("open agent: %v", err)
	}
	f.grants.restricted["ag-hr"] = true
	appended, turns := f.msgs.appended, len(f.queue.payloads)

	_, err := f.enq.Enqueue(context.Background(), onThread("th-hr", "after"))
	if !errors.Is(err, ErrAgentRestricted) {
		t.Fatalf("Enqueue after the restriction: %v, want ErrAgentRestricted", err)
	}
	if !strings.Contains(err.Error(), "HR") {
		t.Errorf("refusal %q does not name the agent", err)
	}
	if f.msgs.appended != appended || len(f.queue.payloads) != turns {
		t.Error("a refused turn wrote a message or enqueued a run")
	}

	f.grants.granted["u-1/ag-hr"] = true
	if _, err := f.enq.Enqueue(context.Background(), onThread("th-hr", "granted")); err != nil {
		t.Errorf("once granted: %v", err)
	}
}

// Seam three, the unpinned half: a conversation that ran as the company default
// refuses when the default is restricted, rather than quietly switching to
// another agent with the first one's answers still in its memory.
func TestAnUnpinnedConversationRefusesRatherThanSwitchingAgent(t *testing.T) {
	f := newAccessFixture(t, "ag-hr", nil, dashboardThread("th-def", ""))
	f.grants.restricted["ag-hr"] = true

	_, err := f.enq.Enqueue(context.Background(), onThread("th-def", "hello again"))
	if !errors.Is(err, ErrAgentRestricted) || !strings.Contains(err.Error(), "HR") {
		t.Fatalf("Enqueue: %v, want ErrAgentRestricted naming HR", err)
	}
	f.assertNothingWritten(t)
}

// Seam four: `@`-addressing. A participant the person may not talk to is refused
// by name — the whole message, not the one name, because a silent drop in a
// room reads as the agent choosing not to answer.
func TestAddressingARestrictedParticipantIsRefusedByName(t *testing.T) {
	room := &stubRoom{participants: []*domain.ThreadParticipant{
		{AgentID: "ag-fin", AgentName: "Finance"},
		{AgentID: "ag-hr", AgentName: "HR"},
	}}
	f := newAccessFixture(t, "ag-fin", room, dashboardThread("th-room", "ag-fin"))
	f.grants.restricted["ag-hr"] = true

	for _, msg := range []string{"@HR what is payroll?", "@Finance @HR both of you"} {
		_, err := f.enq.Enqueue(context.Background(), onThread("th-room", msg))
		if !errors.Is(err, ErrAgentRestricted) || !strings.Contains(err.Error(), "HR") {
			t.Errorf("%q: %v, want ErrAgentRestricted naming HR", msg, err)
		}
	}
	f.assertNothingWritten(t)

	if _, err := f.enq.Enqueue(context.Background(), onThread("th-room", "@Finance just you")); err != nil {
		t.Fatalf("addressing an open participant: %v", err)
	}
	if len(f.queue.payloads) != 1 || f.queue.payloads[0].AgentID != "ag-fin" {
		t.Errorf("payloads = %+v, want one turn for ag-fin", f.queue.payloads)
	}
}

// Seam four, the roster half: an agent the person was never shown is text to
// them, not "not in this conversation" — which would name it.
func TestAnAgentThePersonWasNeverShownIsTextNotARefusal(t *testing.T) {
	roomOf := func() *stubRoom {
		return &stubRoom{
			participants: []*domain.ThreadParticipant{
				{AgentID: "ag-fin", AgentName: "Finance"},
				{AgentID: "ag-ops", AgentName: "Ops"},
			},
			agents: []*domain.Agent{
				{ID: "ag-hr", Name: "HR", Enabled: true},
				{ID: "ag-fin", Name: "Finance", Enabled: true},
				{ID: "ag-ops", Name: "Ops", Enabled: true},
				{ID: "ag-legal", Name: "Legal", Enabled: true},
			},
		}
	}

	open := newAccessFixture(t, "ag-fin", roomOf(), dashboardThread("th-room", "ag-fin"))
	if _, err := open.enq.Enqueue(context.Background(), onThread("th-room", "@Legal can you see contracts?")); !errors.Is(err, ErrAgentNotInRoom) {
		t.Fatalf("an open agent outside the room: %v, want ErrAgentNotInRoom (the control)", err)
	}

	hidden := newAccessFixture(t, "ag-fin", roomOf(), dashboardThread("th-room", "ag-fin"))
	hidden.grants.restricted["ag-legal"] = true
	_, err := hidden.enq.Enqueue(context.Background(), onThread("th-room", "@Legal can you see contracts?"))
	if err != nil {
		t.Fatalf("a restricted agent outside the room: %v, want the message treated as text", err)
	}
	if len(hidden.queue.payloads) != 1 || hidden.queue.payloads[0].AgentID != "ag-fin" {
		t.Errorf("payloads = %+v, want one turn for the room's default speaker", hidden.queue.payloads)
	}
}

// Seam five is forkForAgent, and **it is not a dashboard seam**: it is reached
// only on `/v1` and the widget, the two `user_ref` doors, neither of which has
// an Argentum user. Decision 7 says a grant binds no such door, so T-Z4 asserted
// the opposite of the other four here — a restricted agent is forked to, and the
// grant store is never read — on both doors.
//
// **T-Z8 decided the two doors apart** (roadmap 12, decisions 9 and 10). `/v1`
// still reads no grant, which is what is left here: a key carries its own
// allowlist instead (chat_enqueuer_doors_test.go). The widget now refuses a
// restricted agent, and its case moved to that file, inverted.
func TestADoorWithNoPersonNeverAsks(t *testing.T) {
	latest := func() *domain.ConversationThread {
		return &domain.ConversationThread{
			ID: "th-prev", CompanyID: "co-1", AgentID: "ag-fin", LastMessageAt: time.Now(),
		}
	}
	cases := map[string]ChatInput{
		"the api fork": {
			Channel: domain.ChannelAPI, CompanyID: "co-1", APIUserRef: "ext-1",
			AgentID: "ag-hr", Message: "payroll?",
		},
		// A UserID on a door that is not the dashboard is attribution, not a
		// person whose grants apply.
		"a user id on the api door": {
			Channel: domain.ChannelAPI, CompanyID: "co-1", APIUserRef: "ext-1", UserID: "u-1",
			AgentID: "ag-hr", Message: "payroll?",
		},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAccessFixture(t, "ag-fin", nil)
			f.grants.restricted["ag-hr"] = true
			f.threads.latest, f.threads.latestErr = latest(), nil

			if _, err := f.enq.Enqueue(context.Background(), in); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			if len(f.threads.created) != 1 || f.threads.created[0].AgentID != "ag-hr" {
				t.Fatalf("created %+v, want one forked thread on ag-hr", f.threads.created)
			}
			if f.grants.loads != 0 {
				t.Errorf("the grant store was read %d times on a door with no person", f.grants.loads)
			}
		})
	}
}

// "Every existing single-agent deployment behaves identically — no agent is
// restricted after 084." And the ordinary turn on an existing conversation is
// one grant read, addressed or not.
func TestWithNothingRestrictedEveryTurnRunsAsBefore(t *testing.T) {
	room := &stubRoom{participants: []*domain.ThreadParticipant{
		{AgentID: "ag-fin", AgentName: "Finance"},
		{AgentID: "ag-ops", AgentName: "Ops"},
	}}
	f := newAccessFixture(t, "ag-fin", room, dashboardThread("th-room", "ag-fin"))

	if res, err := f.enq.Enqueue(context.Background(), newChat("hello", "")); err != nil || res.Thread.AgentID != "" {
		t.Fatalf("new conversation, no pick = (%v, %v), want an unpinned thread", res, err)
	}
	if res, err := f.enq.Enqueue(context.Background(), newChat("hello", "ag-ops")); err != nil || res.Thread.AgentID != "ag-ops" {
		t.Fatalf("new conversation on Ops = (%v, %v), want a thread pinned to ag-ops", res, err)
	}

	loads := f.grants.loads
	if _, err := f.enq.Enqueue(context.Background(), onThread("th-room", "plain question")); err != nil {
		t.Fatalf("existing conversation: %v", err)
	}
	if n := f.grants.loads - loads; n != 1 {
		t.Errorf("an ordinary turn read the grant store %d times, want 1", n)
	}

	loads, turns := f.grants.loads, len(f.queue.payloads)
	if _, err := f.enq.Enqueue(context.Background(), onThread("th-room", "@Finance @Ops both")); err != nil {
		t.Fatalf("addressing two: %v", err)
	}
	if n := len(f.queue.payloads) - turns; n != 2 {
		t.Errorf("addressing two agents produced %d turns, want 2", n)
	}
	if n := f.grants.loads - loads; n != 1 {
		t.Errorf("a two-agent turn read the grant store %d times, want 1", n)
	}
}

// A read that fails refuses — and as a retry, never as "restricted" or "no
// such agent", which would send the person to ask for a grant they may hold.
// This is also the one behaviour that changes for a deployment with nothing
// restricted: a default lookup that fails used to leave the worker to resolve
// it (TestNoDefaultAgentStillEnqueuesTheTurn), and with a grant to check that
// default would run unchecked.
func TestAnAccessReadThatFailsRefusesAsARetry(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *accessFixture)
		in    ChatInput
	}{
		{"the grant store, on an existing conversation", func(f *accessFixture) { f.grants.err = errors.New("control DB down") }, onThread("th-fin", "hi")},
		{"the grant store, on a pick", func(f *accessFixture) { f.grants.err = errors.New("control DB down") }, newChat("hi", "ag-fin")},
		{"the grant store, on a new conversation", func(f *accessFixture) { f.grants.err = errors.New("control DB down") }, newChat("hi", "")},
		{"the default lookup, on a new conversation", func(f *accessFixture) { f.roster.err = errors.New("control DB down") }, newChat("hi", "")},
		{"the default lookup, on an unpinned conversation", func(f *accessFixture) { f.roster.err = errors.New("control DB down") }, onThread("th-def", "hi")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAccessFixture(t, "ag-fin", nil, dashboardThread("th-fin", "ag-fin"), dashboardThread("th-def", ""))
			tc.setup(f)
			_, err := f.enq.Enqueue(context.Background(), tc.in)
			if !errors.Is(err, ErrAccessCheckFailed) {
				t.Fatalf("Enqueue: %v, want ErrAccessCheckFailed", err)
			}
			if errors.Is(err, ErrAgentRestricted) || errors.Is(err, domain.ErrNotFound) {
				t.Errorf("a failed read was reported as a refusal: %v", err)
			}
			f.assertNothingWritten(t)
		})
	}
}

// An agent the grant store does not find is let through, as RequireResource
// lets a missing object through to its handler: "restricted" is the wrong
// sentence for a conversation pinned to an agent that was deleted.
func TestAnAgentTheGrantStoreCannotFindIsNotCalledRestricted(t *testing.T) {
	f := newAccessFixture(t, "ag-fin", nil, dashboardThread("th-gone", "ag-gone"))
	if _, err := f.enq.Enqueue(context.Background(), onThread("th-gone", "hi")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := f.queue.payloads[0].AgentID; got != "ag-gone" {
		t.Errorf("turn ran as %q, want the thread's own ag-gone, as before T-Z4", got)
	}
}

// The room: a participant the person may not talk to cannot be added, and is
// refused as not found — the add menu never offered it.
func TestARoomRefusesAnAgentThePersonMayNotTalkTo(t *testing.T) {
	svc, repo := participantFixture(t, "ag-fin")
	grants := newAgentGrants("ag-fin", "ag-ops", "ag-hr", "ag-off")
	grants.restricted["ag-hr"] = true
	grants.restricted["ag-off"] = true
	svc = svc.WithAgentAccess(authz.New(grants))

	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-hr", "u-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("adding a restricted agent: %v, want ErrNotFound", err)
	}
	// Restricted and disabled: still only not found. Saying "disabled" would
	// confirm an agent the person was never shown.
	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-off", "u-1"); !errors.Is(err, domain.ErrNotFound) || errors.Is(err, ErrAgentDisabled) {
		t.Fatalf("adding a restricted, disabled agent: %v, want ErrNotFound and not ErrAgentDisabled", err)
	}
	if len(repo.added) != 0 {
		t.Fatalf("a refused add wrote %d row(s)", len(repo.added))
	}

	grants.granted["u-1/ag-hr"] = true
	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-hr", "u-1"); err != nil {
		t.Fatalf("adding a granted agent: %v", err)
	}

	loads := grants.loads
	if _, err := svc.Add(context.Background(), "co-1", "th-1", "ag-ops", ""); err != nil {
		t.Fatalf("an add with no person: %v", err)
	}
	if grants.loads != loads {
		t.Error("an add with no person read the grant store")
	}
}
