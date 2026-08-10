package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// The widget as a channel (T-20). What these pin is the half a live gate
// cannot: that a widget turn is keyed, forked, attributed and delivered like
// the channel it is, and — the part that would leak — that one visitor's
// thread id is not a way into another's conversation.

func TestWidgetTurnRequiresAnEmbedRef(t *testing.T) {
	// The session token always carries one and the middleware refuses a token
	// without one, so an empty ref here is a wiring bug rather than a caller's
	// choice. It has to fail loudly: a widget thread stored with no ref is a
	// conversation its own visitor can never read back.
	err := ChatInput{
		Channel:   domain.ChannelWidget,
		CompanyID: "co-1",
		Message:   "revenue?",
	}.validate()
	if err == nil {
		t.Fatal("validate accepted a widget turn with no embed_user_ref")
	}
	if !strings.Contains(err.Error(), "embed_user_ref") {
		t.Errorf("err = %v, want it to name the missing field", err)
	}

	if err := (ChatInput{
		Channel:      domain.ChannelWidget,
		CompanyID:    "co-1",
		EmbedUserRef: "emp_812",
		Message:      "revenue?",
	}).validate(); err != nil {
		t.Errorf("validate rejected a well-formed widget turn: %v", err)
	}
}

func TestResolveForEmbedUserKeysAndForks(t *testing.T) {
	t.Run("first message opens a widget thread", func(t *testing.T) {
		repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
		svc := newThreadService(repo, &fakeClassifierLLM{})

		res, err := svc.ResolveForEmbedUser(context.Background(), "co-1", "emp_812", "revenue?", "")
		if err != nil {
			t.Fatalf("ResolveForEmbedUser: %v", err)
		}
		if !res.IsNew {
			t.Fatal("want a new conversation")
		}
		if res.Thread.Channel != domain.ChannelWidget {
			t.Errorf("channel = %q, want widget", res.Thread.Channel)
		}
		if res.Thread.EmbedUserRef != "emp_812" {
			t.Errorf("embed_user_ref = %q, want the visitor we resolved for", res.Thread.EmbedUserRef)
		}
		// The column the api channel uses must stay empty, or the two surfaces
		// start resolving to each other's conversations.
		if res.Thread.APIUserRef != "" {
			t.Errorf("api_user_ref = %q on a widget thread, want empty", res.Thread.APIUserRef)
		}
	})

	t.Run("a warm conversation is continued", func(t *testing.T) {
		warm := &domain.ConversationThread{
			ID: "th-1", CompanyID: "co-1", Channel: domain.ChannelWidget,
			EmbedUserRef: "emp_812", LastMessageAt: time.Now(),
		}
		repo := &fakeThreadRepo{latest: warm}
		svc := newThreadService(repo, &fakeClassifierLLM{})

		res, err := svc.ResolveForEmbedUser(context.Background(), "co-1", "emp_812", "and December?", "")
		if err != nil {
			t.Fatalf("ResolveForEmbedUser: %v", err)
		}
		if res.IsNew || res.Thread.ID != "th-1" {
			t.Errorf("result = %+v, want the same conversation", res.Thread)
		}
	})

	t.Run("companyID and ref are both required", func(t *testing.T) {
		svc := newThreadService(&fakeThreadRepo{}, &fakeClassifierLLM{})
		if _, err := svc.ResolveForEmbedUser(context.Background(), "co-1", "", "hi", ""); err == nil {
			t.Error("resolved a thread for an empty ref")
		}
		if _, err := svc.ResolveForEmbedUser(context.Background(), "", "emp_812", "hi", ""); err == nil {
			t.Error("resolved a thread for an empty company")
		}
	})
}

// A widget visitor who names a different agent forks into another *widget*
// thread, not into an api one. Getting this wrong strands the conversation
// somewhere the visitor's own reads can never find.
func TestForkForAgentKeepsAWidgetTurnOnTheWidget(t *testing.T) {
	warm := &domain.ConversationThread{
		ID: "th-ops", CompanyID: "co-1", Channel: domain.ChannelWidget,
		EmbedUserRef: "emp_812", AgentID: "ag-ops", LastMessageAt: time.Now(),
	}
	repo := &fakeThreadRepo{latest: warm}
	enq := &ChatEnqueuer{threads: newThreadService(repo, &fakeClassifierLLM{})}

	got, err := enq.forkForAgent(context.Background(),
		ChatInput{
			Channel: domain.ChannelWidget, CompanyID: "co-1",
			EmbedUserRef: "emp_812", Message: "revenue?",
		},
		&ResolveResult{Thread: warm, IsNew: false}, "ag-fin")
	if err != nil {
		t.Fatalf("forkForAgent: %v", err)
	}
	if !got.IsNew || got.Thread.ID == warm.ID {
		t.Fatalf("result = %+v, want a new conversation", got.Thread)
	}
	if got.Thread.Channel != domain.ChannelWidget {
		t.Errorf("channel = %q, want the fork to stay on the widget", got.Thread.Channel)
	}
	if got.Thread.EmbedUserRef != "emp_812" {
		t.Errorf("embed_user_ref = %q, want the fork keyed to the same visitor", got.Thread.EmbedUserRef)
	}
	if got.Thread.APIUserRef != "" {
		t.Errorf("api_user_ref = %q — the fork landed on the api channel's key", got.Thread.APIUserRef)
	}
}

// The audit half. A widget turn is a person, but not one of ours: the ref is a
// name the tenant chose and we verified their assertion of it, not the human.
func TestActorOfAttributesAWidgetTurn(t *testing.T) {
	kind, ref := actorOf(queue.ChatRunPayload{
		Channel:      domain.ChannelWidget,
		EmbedUserRef: "emp_812",
		EmbedKeyID:   "ek-1",
	})
	if kind != string(domain.ActorKindEmbed) {
		t.Errorf("actor kind = %q, want embed", kind)
	}
	if ref != "emp_812" {
		t.Errorf("actor ref = %q, want the tenant's own name for the visitor", ref)
	}

	// An API key still outranks it: a turn a script started on behalf of a
	// user_ref is the script's, and that ordering is what stops a widget ref on
	// an unrelated payload silently reattributing an integration's work.
	kind, ref = actorOf(queue.ChatRunPayload{
		APIKeyID: "key-1", EmbedUserRef: "emp_812",
	})
	if kind != string(domain.ActorKindAPIKey) || ref != "key-1" {
		t.Errorf("actor = (%q, %q), want the api key to win", kind, ref)
	}

	if !domain.ActorKindEmbed.Valid() {
		t.Error("ActorKindEmbed is not in the Valid() switch, so the audit write would be rejected")
	}
}
