package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z8: the three doors with no person, decided rather than inherited.
//
// Each test puts the real internal/authz behind the enqueuer, over
// chat_enqueuer_access_test.go's grant store, so what is proven is the rule the
// product runs. The company is that file's: HR, Finance, Ops and Legal, every one
// open until a test restricts it.

func widgetTurn(agentID, threadID string) ChatInput {
	return ChatInput{
		Channel: domain.ChannelWidget, CompanyID: "co-1", EmbedUserRef: "visitor-1",
		AgentID: agentID, ThreadID: threadID, Message: "berapa gaji saya?",
	}
}

func discordIn(channelID string) ChatInput {
	return ChatInput{
		Channel: domain.ChannelDiscord, CompanyID: "co-1",
		DiscordUserID: "user-9", DiscordChannelID: channelID, Message: "payroll for August?",
	}
}

func apiTurn(keyAgents []string, agentID, threadID string) ChatInput {
	in := ChatInput{
		Channel: domain.ChannelAPI, CompanyID: "co-1", AgentID: agentID, ThreadID: threadID,
		KeyAgentIDs: keyAgents, Message: "revenue by branch?",
	}
	if threadID == "" {
		in.APIUserRef = "ext-1"
	}
	return in
}

// --- the widget: decision 10 -------------------------------------------

// "The widget never reaches a restricted resource, and this is the one door
// where refusing is right." The roadmap put the refusal at embed-key save time,
// but an embed key names no agent — the visitor's browser picks one — so it is
// at the pick and at the turn, and it holds whatever the visitor's ref happens to
// be called.
func TestTheWidgetNeverReachesARestrictedAgent(t *testing.T) {
	t.Run("a pick of one is not found, like an agent that does not exist", func(t *testing.T) {
		f := newAccessFixture(t, "ag-fin", nil)
		f.grants.restricted["ag-hr"] = true
		// A person granted HR whose id is the visitor's ref: a visitor is nobody,
		// and a string that matches a user id makes them nobody in particular.
		f.grants.granted["visitor-1/ag-hr"] = true

		_, err := f.enq.Enqueue(context.Background(), widgetTurn("ag-hr", ""))
		if !errors.Is(err, ErrAgentNotFound) {
			t.Fatalf("Enqueue = %v, want ErrAgentNotFound", err)
		}
		if len(f.threads.created) != 0 || f.msgs.appended != 0 || len(f.queue.payloads) != 0 {
			t.Errorf("a refused pick wrote: %d threads, %d messages, %d turns",
				len(f.threads.created), f.msgs.appended, len(f.queue.payloads))
		}
	})

	t.Run("a conversation already running as one is refused, and writes nothing", func(t *testing.T) {
		th := &domain.ConversationThread{
			ID: "th-w", CompanyID: "co-1", Channel: domain.ChannelWidget,
			EmbedUserRef: "visitor-1", AgentID: "ag-hr",
		}
		f := newAccessFixture(t, "ag-fin", nil, th)
		f.grants.restricted["ag-hr"] = true

		_, err := f.enq.Enqueue(context.Background(), widgetTurn("", "th-w"))
		if !errors.Is(err, ErrAgentNotClearedHere) {
			t.Fatalf("Enqueue = %v, want ErrAgentNotClearedHere", err)
		}
		if f.msgs.appended != 0 || len(f.queue.payloads) != 0 {
			t.Errorf("a refused turn appended %d messages and queued %d turns", f.msgs.appended, len(f.queue.payloads))
		}
	})
}

// The dashboard's opening rule (T-Z4), for nobody: a restricted default is not a
// visitor's default, and the first open agent in the roster's order is — pinned,
// so the conversation is not refused on its own second message.
func TestAWidgetWithARestrictedDefaultOpensOnTheFirstOpenAgent(t *testing.T) {
	recent := func(agentID string) *domain.ConversationThread {
		return &domain.ConversationThread{
			ID: "th-prev", CompanyID: "co-1", Channel: domain.ChannelWidget,
			EmbedUserRef: "visitor-1", AgentID: agentID, LastMessageAt: time.Now(),
		}
	}

	t.Run("a new conversation is pinned to it", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		f.grants.restricted["ag-hr"] = true

		res, err := f.enq.Enqueue(context.Background(), widgetTurn("", ""))
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if res.Thread.AgentID != "ag-fin" || len(f.queue.payloads) != 1 || f.queue.payloads[0].AgentID != "ag-fin" {
			t.Errorf("thread on %q, payloads %+v; want both on Finance, the first open agent", res.Thread.AgentID, f.queue.payloads)
		}
	})

	t.Run("a conversation that ran as that default moves onto it", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		f.grants.restricted["ag-hr"] = true
		f.threads.latest, f.threads.latestErr = recent(""), nil

		if _, err := f.enq.Enqueue(context.Background(), widgetTurn("", "")); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if len(f.threads.created) != 1 || f.threads.created[0].AgentID != "ag-fin" {
			t.Errorf("created %+v, want one conversation forked onto Finance", f.threads.created)
		}
	})

	t.Run("one pinned to an open agent stays where it is", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		f.grants.restricted["ag-hr"] = true
		f.threads.latest, f.threads.latestErr = recent("ag-ops"), nil

		if _, err := f.enq.Enqueue(context.Background(), widgetTurn("", "")); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if len(f.threads.created) != 0 || f.queue.payloads[0].AgentID != "ag-ops" {
			t.Errorf("created %+v and ran as %q; want the Ops conversation continued", f.threads.created, f.queue.payloads[0].AgentID)
		}
	})

	t.Run("with every agent restricted nothing is opened", func(t *testing.T) {
		f := newAccessFixture(t, "ag-hr", nil)
		for _, id := range []string{"ag-hr", "ag-fin", "ag-ops", "ag-legal"} {
			f.grants.restricted[id] = true
		}
		_, err := f.enq.Enqueue(context.Background(), widgetTurn("", ""))
		if !errors.Is(err, ErrNoAgentAvailable) {
			t.Fatalf("Enqueue = %v, want ErrNoAgentAvailable", err)
		}
		if len(f.threads.created) != 0 {
			t.Errorf("created %d threads for a visitor nothing is open to", len(f.threads.created))
		}
	})
}

func TestWithNothingRestrictedTheWidgetRunsAsBefore(t *testing.T) {
	f := newAccessFixture(t, "ag-fin", nil)

	res, err := f.enq.Enqueue(context.Background(), widgetTurn("", ""))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if res.Thread.AgentID != "" || f.queue.payloads[0].AgentID != "ag-fin" {
		t.Errorf("thread pinned to %q, ran as %q; want unpinned, on the default", res.Thread.AgentID, f.queue.payloads[0].AgentID)
	}
	if res, err := f.enq.Enqueue(context.Background(), widgetTurn("ag-ops", "")); err != nil || f.queue.payloads[1].AgentID != "ag-ops" {
		t.Errorf("a pick of an open agent = (%v, %v), want it honoured", res, err)
	}
}

// --- the channels: decision 8 ------------------------------------------

// "On a channel, the channel is the grant — and the admin is told so." A binding
// clears its address for a restricted agent once an admin acknowledged it, and
// nothing else does: not a binding made before the restriction, and not the
// company default on an address nobody bound.
func TestAChannelReachesARestrictedAgentOnlyWhereAnAdminAcknowledgedIt(t *testing.T) {
	const addr = "co-1/discord/chan-hr"
	cases := []struct {
		name      string
		def       string
		bound     map[string]string
		acked     map[string]bool
		wantAgent string
		wantErr   error
	}{
		{name: "bound and acknowledged", def: "ag-fin", bound: map[string]string{addr: "ag-hr"},
			acked: map[string]bool{addr: true}, wantAgent: "ag-hr"},
		{name: "bound before the restriction, and nobody acknowledged it", def: "ag-fin",
			bound: map[string]string{addr: "ag-hr"}, wantErr: ErrAgentNotClearedHere},
		{name: "unbound, where the company default is restricted", def: "ag-hr", wantErr: ErrAgentNotClearedHere},
		{name: "bound to an open agent, acknowledged or not", def: "ag-hr",
			bound: map[string]string{addr: "ag-ops"}, wantAgent: "ag-ops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAccessFixture(t, tc.def, nil)
			f.grants.restricted["ag-hr"] = true
			f.enq.WithChannelBindings(&stubBinder{bound: tc.bound, acked: tc.acked})

			_, err := f.enq.Enqueue(context.Background(), discordIn("chan-hr"))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Enqueue = %v, want %v", err, tc.wantErr)
				}
				if f.msgs.appended != 0 || len(f.queue.payloads) != 0 {
					t.Errorf("a refused channel turn appended %d messages and queued %d turns", f.msgs.appended, len(f.queue.payloads))
				}
				return
			}
			if err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			if len(f.queue.payloads) != 1 || f.queue.payloads[0].AgentID != tc.wantAgent {
				t.Errorf("payloads = %+v, want one turn on %s", f.queue.payloads, tc.wantAgent)
			}
		})
	}
}

// The acknowledged agent is not asked about at all: whatever its mode, the
// address may reach it, so a read could only cost time.
func TestAnAcknowledgedBindingReadsNoGrant(t *testing.T) {
	const addr = "co-1/discord/chan-hr"
	f := newAccessFixture(t, "ag-fin", nil)
	f.grants.restricted["ag-hr"] = true
	f.enq.WithChannelBindings(&stubBinder{bound: map[string]string{addr: "ag-hr"}, acked: map[string]bool{addr: true}})

	if _, err := f.enq.Enqueue(context.Background(), discordIn("chan-hr")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if f.grants.loads != 0 {
		t.Errorf("the grant store was read %d times for an agent the binding already cleared", f.grants.loads)
	}
}

// The four chat handlers say a refusal back rather than answering the platform
// 500, which WhatsApp, Lark and Slack retry.
func TestAChannelRefusalIsSpokenNotRetried(t *testing.T) {
	cases := []struct {
		err  error
		want string
		ok   bool
	}{
		{fmt.Errorf("resolve thread: %w", ErrAgentNotClearedHere), AgentNotClearedMessage, true},
		{fmt.Errorf("%w: %s", domain.ErrInsufficientCredits, CreditsExhaustedMessage), CreditsExhaustedMessage, true},
		{fmt.Errorf("%w: control DB down", ErrAccessCheckFailed), "", false},
		{errors.New("enqueue chat:run: redis down"), "", false},
	}
	for _, tc := range cases {
		got, ok := SpokenRefusal(tc.err)
		if ok != tc.ok || got != tc.want {
			t.Errorf("SpokenRefusal(%v) = (%q, %v), want (%q, %v)", tc.err, got, ok, tc.want, tc.ok)
		}
	}
}

// --- /v1: decision 9 ---------------------------------------------------

// "A key with an allowlist cannot reach an agent outside it; an empty allowlist
// reaches every agent, exactly as today." The empty half — a restricted agent
// forked to with no grant read — is TestADoorWithNoPersonNeverAsks.
func TestAKeyWithAnAgentListReachesNothingOutsideIt(t *testing.T) {
	t.Run("a pick outside the list is not found, and opens nothing", func(t *testing.T) {
		f := newAccessFixture(t, "ag-fin", nil)
		_, err := f.enq.Enqueue(context.Background(), apiTurn([]string{"ag-fin"}, "ag-ops", ""))
		if !errors.Is(err, ErrAgentNotFound) {
			t.Fatalf("Enqueue = %v, want ErrAgentNotFound", err)
		}
		if len(f.threads.created) != 0 || f.msgs.appended != 0 {
			t.Errorf("a refused pick wrote %d threads and %d messages", len(f.threads.created), f.msgs.appended)
		}
	})

	t.Run("a pick on the list runs — a restricted one included — and reads no grant", func(t *testing.T) {
		f := newAccessFixture(t, "ag-fin", nil)
		f.grants.restricted["ag-hr"] = true
		if _, err := f.enq.Enqueue(context.Background(), apiTurn([]string{"AG-HR"}, "ag-hr", "")); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if f.queue.payloads[0].AgentID != "ag-hr" || f.grants.loads != 0 {
			t.Errorf("ran as %q with %d grant reads; want HR, and none — a key borrows no grants", f.queue.payloads[0].AgentID, f.grants.loads)
		}
	})

	t.Run("a conversation running as an agent outside the list is refused", func(t *testing.T) {
		th := &domain.ConversationThread{ID: "th-api", CompanyID: "co-1", Channel: domain.ChannelAPI, AgentID: "ag-ops"}
		f := newAccessFixture(t, "ag-fin", nil, th)
		_, err := f.enq.Enqueue(context.Background(), apiTurn([]string{"ag-fin"}, "", "th-api"))
		if !errors.Is(err, ErrAgentNotAllowed) {
			t.Fatalf("Enqueue = %v, want ErrAgentNotAllowed", err)
		}
		if f.msgs.appended != 0 || len(f.queue.payloads) != 0 {
			t.Errorf("a refused turn appended %d messages and queued %d turns", f.msgs.appended, len(f.queue.payloads))
		}
	})

	t.Run("a call naming no agent, whose default is not on the list, opens no conversation", func(t *testing.T) {
		f := newAccessFixture(t, "ag-fin", nil)
		_, err := f.enq.Enqueue(context.Background(), apiTurn([]string{"ag-ops"}, "", ""))
		if !errors.Is(err, ErrAgentNotAllowed) {
			t.Fatalf("Enqueue = %v, want ErrAgentNotAllowed", err)
		}
		if len(f.threads.created) != 0 {
			t.Errorf("a refused call left %d conversations behind", len(f.threads.created))
		}
	})

	t.Run("a call naming no agent, whose default is on the list, runs as it", func(t *testing.T) {
		f := newAccessFixture(t, "ag-fin", nil)
		if _, err := f.enq.Enqueue(context.Background(), apiTurn([]string{"ag-fin", "ag-ops"}, "", "")); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if f.queue.payloads[0].AgentID != "ag-fin" {
			t.Errorf("ran as %q, want the default", f.queue.payloads[0].AgentID)
		}
	})
}
