package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-S4: which agent answers a message that arrived on a channel. The dashboard
// and `/v1` are told; Discord, Lark and WhatsApp are looked up, from the address
// the message came in on.

// stubBinder answers AgentForChannel from a map keyed exactly the way the
// unique index is — company, channel, address — so a lookup that gets any of
// the three wrong misses, which is the failure this stub exists to catch.
type stubBinder struct {
	bound map[string]string
	err   error
	asked []string
}

func (s *stubBinder) AgentForChannel(
	_ context.Context, companyID string, channel domain.Channel, externalID string,
) (string, error) {
	key := companyID + "/" + string(channel) + "/" + externalID
	s.asked = append(s.asked, key)
	if s.err != nil {
		return "", s.err
	}
	if id, ok := s.bound[key]; ok {
		return id, nil
	}
	return "", domain.ErrNotFound
}

func TestABoundChannelRunsAsItsAgent(t *testing.T) {
	binder := &stubBinder{bound: map[string]string{"co-1/discord/chan-ops": "ag-ops"}}
	enq := (&ChatEnqueuer{}).WithChannelBindings(binder)

	got, err := enq.boundAgent(context.Background(), ChatInput{
		Channel: domain.ChannelDiscord, CompanyID: "co-1",
		DiscordUserID: "user-9", DiscordChannelID: "chan-ops",
	})
	if err != nil {
		t.Fatalf("boundAgent: %v", err)
	}
	if got != "ag-ops" {
		t.Errorf("boundAgent = %q, want ag-ops", got)
	}
}

// The binding is on the room, not on the person. A lookup keyed by the Discord
// *user* would bind whoever asked first and answer everyone else as them.
func TestTheDiscordBindingIsOnTheChannelNotTheUser(t *testing.T) {
	binder := &stubBinder{}
	enq := (&ChatEnqueuer{}).WithChannelBindings(binder)

	if _, err := enq.boundAgent(context.Background(), ChatInput{
		Channel: domain.ChannelDiscord, CompanyID: "co-1",
		DiscordUserID: "user-9", DiscordChannelID: "chan-ops",
	}); err != nil {
		t.Fatalf("boundAgent: %v", err)
	}
	if len(binder.asked) != 1 || binder.asked[0] != "co-1/discord/chan-ops" {
		t.Errorf("asked %v, want one lookup on the channel id", binder.asked)
	}
}

// The write path stores a normalised number and the wire delivers `whatsapp:`
// prefixes. One function normalises both ends, and this is what proves the read
// end calls it: without it the binding exists and never fires, with nothing to
// see in either the table or the log.
func TestAWhatsAppBindingMatchesAPrefixedInboundNumber(t *testing.T) {
	binder := &stubBinder{bound: map[string]string{"co-1/whatsapp/+628123": "ag-fin"}}
	enq := (&ChatEnqueuer{}).WithChannelBindings(binder)

	got, err := enq.boundAgent(context.Background(), ChatInput{
		Channel: domain.ChannelWhatsApp, CompanyID: "co-1", PhoneNumber: " whatsapp:+628123 ",
	})
	if err != nil {
		t.Fatalf("boundAgent: %v", err)
	}
	if got != "ag-fin" {
		t.Errorf("boundAgent = %q, want ag-fin for a prefixed inbound number", got)
	}
}

func TestAnUnboundChannelAsksForNoAgent(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithChannelBindings(&stubBinder{})

	got, err := enq.boundAgent(context.Background(), ChatInput{
		Channel: domain.ChannelLark, CompanyID: "co-1", LarkChatID: "oc-general",
	})
	if err != nil {
		t.Fatalf("boundAgent: %v", err)
	}
	if got != "" {
		t.Errorf("boundAgent = %q, want empty so the company default answers", got)
	}
}

// Deliberately the opposite of agentFor's fail-open. A failed default lookup
// leaves the field empty and the worker resolves the same default, so nothing
// widens; a failed *binding* lookup would answer a question asked in the
// finance room with an agent that can read every source the company has.
func TestAFailedBindingLookupStopsTheTurn(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithChannelBindings(&stubBinder{err: errors.New("control DB down")})

	if _, err := enq.boundAgent(context.Background(), ChatInput{
		Channel: domain.ChannelDiscord, CompanyID: "co-1", DiscordChannelID: "chan-ops",
	}); err == nil {
		t.Fatal("boundAgent swallowed a lookup failure; a scope must not widen on an outage")
	}
}

// A deployment without the bindings wired answers exactly as it did before this
// ticket: every channel turn on the company default.
func TestNoBinderWiredIsEveryChannelOnTheDefault(t *testing.T) {
	got, err := (&ChatEnqueuer{}).boundAgent(context.Background(), ChatInput{
		Channel: domain.ChannelDiscord, CompanyID: "co-1", DiscordChannelID: "chan-ops",
	})
	if err != nil {
		t.Fatalf("boundAgent: %v", err)
	}
	if got != "" {
		t.Errorf("boundAgent = %q, want empty", got)
	}
}

// --- the fork ----------------------------------------------------------

func channelEnqueuer(repo *fakeThreadRepo, defaultAgent string) *ChatEnqueuer {
	svc := newThreadService(repo, &fakeClassifierLLM{reply: "RELATED"})
	enq := (&ChatEnqueuer{threads: svc})
	if defaultAgent != "" {
		enq = enq.WithRoster(&stubDefaultAgent{agent: &domain.Agent{ID: defaultAgent}})
	}
	return enq
}

func discordInput() ChatInput {
	return ChatInput{
		Channel: domain.ChannelDiscord, CompanyID: "co-1",
		DiscordUserID: "user-9", DiscordChannelID: "chan-ops", Message: "how were sales?",
	}
}

// The case this whole function exists for. A Discord thread is keyed by
// (company, user), so one person asking in #ops and then in #finance is one
// thread — and continuing it would answer as the first room's agent with the
// first room's answers still in memory.
func TestABindingThatDisagreesWithTheThreadForksIt(t *testing.T) {
	repo := &fakeThreadRepo{}
	enq := channelEnqueuer(repo, "ag-def")
	thread := &domain.ConversationThread{ID: "existing", CompanyID: "co-1", AgentID: "ag-fin"}

	res, err := enq.rebindThread(context.Background(), discordInput(),
		&ResolveResult{Thread: thread}, "ag-ops")
	if err != nil {
		t.Fatalf("rebindThread: %v", err)
	}
	if !res.IsNew {
		t.Fatal("the conversation continued under the wrong agent")
	}
	created := repo.created[0]
	if created.AgentID != "ag-ops" {
		t.Errorf("forked thread agent = %q, want ag-ops", created.AgentID)
	}
	if created.DiscordUserID != "user-9" || created.Channel != domain.ChannelDiscord {
		t.Errorf("forked thread = %+v, want the same Discord identity", created)
	}
}

func TestAThreadAlreadyOnTheBoundAgentIsContinued(t *testing.T) {
	repo := &fakeThreadRepo{}
	enq := channelEnqueuer(repo, "ag-def")
	thread := &domain.ConversationThread{ID: "existing", CompanyID: "co-1", AgentID: "ag-ops"}

	res, err := enq.rebindThread(context.Background(), discordInput(),
		&ResolveResult{Thread: thread}, "ag-ops")
	if err != nil {
		t.Fatalf("rebindThread: %v", err)
	}
	if res.IsNew || len(repo.created) != 0 {
		t.Error("a conversation already on the bound agent was forked")
	}
}

// The ordinary company: no bindings, no pinned threads, and nothing to fork.
// Both sides of the comparison resolve NULL to the same default, which is what
// keeps this from forking every conversation on every message.
func TestAnUnboundChannelWithAnUnpinnedThreadNeverForks(t *testing.T) {
	repo := &fakeThreadRepo{}
	enq := channelEnqueuer(repo, "ag-def")
	thread := &domain.ConversationThread{ID: "existing", CompanyID: "co-1"}

	res, err := enq.rebindThread(context.Background(), discordInput(), &ResolveResult{Thread: thread}, "")
	if err != nil {
		t.Fatalf("rebindThread: %v", err)
	}
	if res.IsNew || len(repo.created) != 0 {
		t.Error("an unbound channel forked a conversation that was already on the default")
	}
}

// Removing a binding has to reach the conversations it pinned. "Unbound" means
// the company default, not "no opinion" — otherwise a channel keeps answering
// as an agent nobody has configured for it since the day the row was deleted.
func TestRemovingABindingReturnsTheChannelToTheDefault(t *testing.T) {
	repo := &fakeThreadRepo{}
	enq := channelEnqueuer(repo, "ag-def")
	thread := &domain.ConversationThread{ID: "existing", CompanyID: "co-1", AgentID: "ag-ops"}

	res, err := enq.rebindThread(context.Background(), discordInput(), &ResolveResult{Thread: thread}, "")
	if err != nil {
		t.Fatalf("rebindThread: %v", err)
	}
	if !res.IsNew {
		t.Fatal("the conversation stayed on the agent of a binding that no longer exists")
	}
	if repo.created[0].AgentID != "ag-def" {
		t.Errorf("forked thread agent = %q, want the company default", repo.created[0].AgentID)
	}
}

// A thread the resolver has just created is already pinned to the binding, so
// there is nothing to compare and nothing to fork. Forking here would leave an
// empty conversation behind on every first message.
func TestANewThreadIsNeverForked(t *testing.T) {
	repo := &fakeThreadRepo{}
	enq := channelEnqueuer(repo, "ag-def")
	thread := &domain.ConversationThread{ID: "new-thread", CompanyID: "co-1", AgentID: "ag-ops"}

	res, err := enq.rebindThread(context.Background(), discordInput(),
		&ResolveResult{Thread: thread, IsNew: true}, "ag-ops")
	if err != nil {
		t.Fatalf("rebindThread: %v", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("created %d extra threads for a brand-new conversation", len(repo.created))
	}
	if res.Thread.ID != "new-thread" {
		t.Errorf("thread = %q, want the one the resolver just made", res.Thread.ID)
	}
}

// --- pinning on create -------------------------------------------------

func TestAFirstMessageInABoundChannelPinsTheThread(t *testing.T) {
	repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
	enq := channelEnqueuer(repo, "ag-def")

	if _, err := enq.resolveChannelThread(context.Background(), discordInput(), "ag-ops"); err != nil {
		t.Fatalf("resolveChannelThread: %v", err)
	}
	if got := repo.created[0].AgentID; got != "ag-ops" {
		t.Errorf("created thread agent = %q, want ag-ops", got)
	}
}

// The idle-gap fork is the same conversation arriving after a gap. Re-resolving
// the default there would widen its scope back to the default agent's without
// anybody changing a binding — the direction T-S2 refused for a disabled agent,
// arriving through the clock instead.
func TestAnIdleGapForkKeepsTheParentsAgent(t *testing.T) {
	latest := &domain.ConversationThread{
		ID: "existing", CompanyID: "co-1", Channel: domain.ChannelDiscord,
		DiscordUserID: "user-9", AgentID: "ag-ops",
		LastMessageAt: time.Now().Add(-90 * time.Minute),
	}
	repo := &fakeThreadRepo{latest: latest}
	svc := newThreadService(repo, &fakeClassifierLLM{reply: "NEW"})

	res, err := svc.ResolveForDiscordUser(context.Background(), "co-1", "user-9", "something else", "")
	if err != nil {
		t.Fatalf("ResolveForDiscordUser: %v", err)
	}
	if !res.IsNew {
		t.Fatal("expected a fork on an unrelated topic after the idle gap")
	}
	if got := repo.created[0].AgentID; got != "ag-ops" {
		t.Errorf("forked thread agent = %q, want the parent's ag-ops", got)
	}
}

// And a binding still wins over the parent: an admin who re-pointed the channel
// changed what answers there, and the fork is where that takes effect.
func TestABindingOutranksTheParentsAgentOnAFork(t *testing.T) {
	latest := &domain.ConversationThread{
		ID: "existing", CompanyID: "co-1", Channel: domain.ChannelDiscord,
		DiscordUserID: "user-9", AgentID: "ag-ops",
		LastMessageAt: time.Now().Add(-90 * time.Minute),
	}
	repo := &fakeThreadRepo{latest: latest}
	svc := newThreadService(repo, &fakeClassifierLLM{reply: "NEW"})

	if _, err := svc.ResolveForDiscordUser(
		context.Background(), "co-1", "user-9", "something else", "ag-fin"); err != nil {
		t.Fatalf("ResolveForDiscordUser: %v", err)
	}
	if got := repo.created[0].AgentID; got != "ag-fin" {
		t.Errorf("forked thread agent = %q, want the binding's ag-fin", got)
	}
}
