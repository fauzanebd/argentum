package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z10: a conversation is as restricted as the agents in it. The grant store is
// agentGrants from the T-Z4 tests, behind the real Authorizer.

// conversationAgents is ConversationAgentLoader over a fixed map. A thread
// absent from the map is one the company does not have.
type conversationAgents struct {
	byThread map[string][]string
	err      error
	calls    int
}

func (f *conversationAgents) AgentsByThread(_ context.Context, _ string, ids []string) (map[string][]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string][]string{}
	for _, id := range ids {
		if agents, ok := f.byThread[id]; ok {
			out[id] = agents
		}
	}
	return out, nil
}

func conversationsFixture(defaultID string) (*ConversationAccess, *conversationAgents, *agentGrants, *stubDefaultAgent) {
	loader := &conversationAgents{byThread: map[string][]string{
		"th-fin":        {"ag-fin"},
		"th-hr":         {"ag-hr"},
		"th-room":       {"ag-fin", "ag-hr"}, // HR in the room
		"th-said":       {"ag-fin", "ag-hr"}, // HR answered, then left the room
		"th-unpinned":   {},                  // runs as the company default
		"th-ghost":      {"ag-fin", "ag-gone"},
		"th-ops":        {"ag-ops"},
		"th-fin-and-op": {"ag-fin", "ag-ops"},
	}}
	grants := newAgentGrants("ag-hr", "ag-fin", "ag-ops")
	roster := &stubDefaultAgent{agent: &domain.Agent{ID: defaultID}}
	if defaultID == "" {
		roster = &stubDefaultAgent{err: domain.ErrNotFound}
	}
	return NewConversationAccess(loader, authz.New(grants), roster), loader, grants, roster
}

var everyConversation = []string{"th-fin", "th-hr", "th-room", "th-said", "th-unpinned", "th-ghost", "th-ops", "th-fin-and-op"}

func readable(t *testing.T, ca *ConversationAccess, userID string, ids ...string) []string {
	t.Helper()
	got, err := ca.Readable(context.Background(), "co-1", userID, ids)
	if err != nil {
		t.Fatalf("Readable: %v", err)
	}
	return got
}

// Nothing restricted: every conversation, exactly as before — the property
// that makes this invisible to every tenant who has not restricted anything.
func TestWithNothingRestrictedEveryConversationIsReadable(t *testing.T) {
	ca, _, _, _ := conversationsFixture("ag-fin")
	if got := readable(t, ca, "u-1", everyConversation...); !slices.Equal(got, []string{
		"th-fin", "th-hr", "th-room", "th-said", "th-unpinned", "th-ghost", "th-ops", "th-fin-and-op",
	}) {
		t.Errorf("Readable = %v, want every conversation the company has", got)
	}
}

// The rule, case by case, for a person not granted HR.
func TestAConversationIsAsRestrictedAsTheAgentsInIt(t *testing.T) {
	ca, _, grants, _ := conversationsFixture("ag-hr")
	grants.restricted["ag-hr"] = true

	want := []string{
		"th-fin",
		// th-hr: its own agent.
		// th-room: HR is in the room, though the conversation runs as Finance.
		// th-said: HR left the room and its answers did not.
		// th-unpinned: runs as the company default, which is HR.
		"th-ghost", // an agent that no longer exists restricts nothing
		"th-ops",
		"th-fin-and-op",
	}
	if got := readable(t, ca, "u-1", everyConversation...); !slices.Equal(got, want) {
		t.Errorf("Readable = %v\nwant       %v", got, want)
	}

	// The grant opens every one of them, and somebody else's opens none.
	grants.granted["u-2/ag-hr"] = true
	if got := readable(t, ca, "u-1", everyConversation...); !slices.Equal(got, want) {
		t.Errorf("another person's grant changed what u-1 reads: %v", got)
	}
	grants.granted["u-1/ag-hr"] = true
	if got := readable(t, ca, "u-1", everyConversation...); len(got) != len(everyConversation) {
		t.Errorf("granted HR, Readable = %v, want all %d", got, len(everyConversation))
	}
}

// Every agent must be open, not any: a room of Finance and Ops is hidden from a
// person who may use only Finance.
func TestEveryAgentInTheConversationMustBeOpenToThePerson(t *testing.T) {
	ca, _, grants, _ := conversationsFixture("ag-fin")
	grants.restricted["ag-ops"] = true
	got := readable(t, ca, "u-1", "th-fin", "th-fin-and-op", "th-ops")
	if !slices.Equal(got, []string{"th-fin"}) {
		t.Errorf("Readable = %v, want only th-fin", got)
	}
}

// A company with no roster has no default to be restricted, so an unattributed
// conversation stays readable.
func TestAnUnattributedConversationInACompanyWithNoRosterIsReadable(t *testing.T) {
	ca, _, _, _ := conversationsFixture("")
	if got := readable(t, ca, "u-1", "th-unpinned"); !slices.Equal(got, []string{"th-unpinned"}) {
		t.Errorf("Readable = %v, want th-unpinned", got)
	}
}

// Not the company's, or not there: not returned.
func TestAConversationTheCompanyDoesNotHaveIsNotReadable(t *testing.T) {
	ca, _, _, _ := conversationsFixture("ag-fin")
	if got := readable(t, ca, "u-1", "th-fin", "th-other-company"); !slices.Equal(got, []string{"th-fin"}) {
		t.Errorf("Readable = %v, want only th-fin", got)
	}
	if ok, err := ca.MayRead(context.Background(), "co-1", "u-1", "th-other-company"); ok || err != nil {
		t.Errorf("MayRead(another company's thread) = (%v, %v), want (false, nil)", ok, err)
	}
}

// A page of conversations is one agents load and one grant read — plus one
// decision for the agent the grant read did not admit, to tell restricted from
// deleted. Not one round trip per conversation.
func TestAPageOfConversationsIsReadInAFewLoadsNotOnePerConversation(t *testing.T) {
	ca, loader, grants, roster := conversationsFixture("ag-hr")
	grants.restricted["ag-hr"] = true
	readable(t, ca, "u-1", everyConversation...)
	if loader.calls != 1 {
		t.Errorf("agents loaded %d times for %d conversations, want 1", loader.calls, len(everyConversation))
	}
	// One Visible over the page's agents, then one Decide each for ag-hr
	// (restricted) and ag-gone (deleted).
	if grants.loads != 3 {
		t.Errorf("grant store read %d times, want 3", grants.loads)
	}
	if roster.calls != 1 {
		t.Errorf("the default was looked up %d times, want once however many conversations need it", roster.calls)
	}
}

// Ids arrive from URLs; a capitalised one is the same conversation.
func TestAConversationIdIsMatchedWhateverItsCase(t *testing.T) {
	ca, _, _, _ := conversationsFixture("ag-fin")
	if ok, err := ca.MayRead(context.Background(), "co-1", "u-1", "TH-FIN"); !ok || err != nil {
		t.Errorf("MayRead(TH-FIN) = (%v, %v), want (true, nil)", ok, err)
	}
}

// Every failed read is an error, never an answer.
func TestAConversationReadThatFailsIsAnErrorNotAnAnswer(t *testing.T) {
	cases := map[string]func(*conversationAgents, *agentGrants, *stubDefaultAgent){
		"the agents":      func(l *conversationAgents, _ *agentGrants, _ *stubDefaultAgent) { l.err = errors.New("down") },
		"the grant store": func(_ *conversationAgents, g *agentGrants, _ *stubDefaultAgent) { g.err = errors.New("down") },
		"the default":     func(_ *conversationAgents, _ *agentGrants, r *stubDefaultAgent) { r.err = errors.New("down") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			ca, loader, grants, roster := conversationsFixture("ag-fin")
			breakIt(loader, grants, roster)
			got, err := ca.Readable(context.Background(), "co-1", "u-1", everyConversation)
			if !errors.Is(err, ErrAccessCheckFailed) || got != nil {
				t.Errorf("Readable = (%v, %v), want (nil, ErrAccessCheckFailed)", got, err)
			}
			if ok, err := ca.MayRead(context.Background(), "co-1", "u-1", "th-unpinned"); ok || err == nil {
				t.Errorf("MayRead = (%v, %v), want (false, error)", ok, err)
			}
		})
	}
}

// No person, or nothing wired: everything, and nothing read.
func TestNoPersonOrNoWiringReadsEverything(t *testing.T) {
	ca, loader, grants, _ := conversationsFixture("ag-hr")
	grants.restricted["ag-hr"] = true
	if got := readable(t, ca, "", "th-hr"); !slices.Equal(got, []string{"th-hr"}) {
		t.Errorf("no person: Readable = %v", got)
	}
	if loader.calls != 0 || grants.loads != 0 {
		t.Error("a read with no person consulted a store")
	}
	var unwired *ConversationAccess
	if ok, err := unwired.MayRead(context.Background(), "co-1", "u-1", "th-hr"); !ok || err != nil {
		t.Errorf("nil ConversationAccess MayRead = (%v, %v), want (true, nil)", ok, err)
	}
}

// Sending into a conversation the person may not read is refused as a missing
// conversation, before anything is written — or its restricted answers would be
// replayed into the new turn's memory and could be asked for by name.
func TestSendingIntoAConversationThePersonMayNotReadIsRefused(t *testing.T) {
	f := newAccessFixture(t, "ag-fin", nil, dashboardThread("th-said", "ag-fin"))
	f.grants.restricted["ag-hr"] = true
	loader := &conversationAgents{byThread: map[string][]string{"th-said": {"ag-fin", "ag-hr"}}}
	f.enq = f.enq.WithConversationAccess(NewConversationAccess(loader, authz.New(f.grants), f.roster))

	// T-Z4's check alone would let this through: the turn runs as Finance.
	_, err := f.enq.Enqueue(context.Background(), onThread("th-said", "what did HR say above?"))
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("Enqueue: %v, want ErrConversationNotFound", err)
	}
	f.assertNothingWritten(t)

	f.grants.granted["u-1/ag-hr"] = true
	if _, err := f.enq.Enqueue(context.Background(), onThread("th-said", "what did HR say above?")); err != nil {
		t.Errorf("once granted: %v", err)
	}
}
