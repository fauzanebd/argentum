package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The `api` channel (T-A1). These tests exist because a missing switch case is
// a silent no-op — the playbook's own warning — and because the two consumers
// that need this channel (T-A2's agentic report door, T-A3's chat) are written
// against it before either exists to exercise it.

func TestAPIChannelInputValidation(t *testing.T) {
	cases := []struct {
		name    string
		in      ChatInput
		wantErr string
	}{
		{
			name: "a user reference is enough",
			in: ChatInput{Channel: domain.ChannelAPI, CompanyID: "co-1",
				APIUserRef: "their-user-42", Message: "sales last month?"},
		},
		{
			name: "a thread id is enough",
			in: ChatInput{Channel: domain.ChannelAPI, CompanyID: "co-1",
				ThreadID: "th-1", Message: "and the month before?"},
		},
		{
			// A turn with neither is billed to the company with nothing in
			// usage/by-user to say who spent it.
			name: "neither is refused",
			in: ChatInput{Channel: domain.ChannelAPI, CompanyID: "co-1",
				Message: "sales last month?"},
			wantErr: "api_user_ref or thread_id required",
		},
		{
			name:    "an empty message is refused like every other channel",
			in:      ChatInput{Channel: domain.ChannelAPI, CompanyID: "co-1", APIUserRef: "u", Message: "  "},
			wantErr: "message required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveForAPIUserCreatesAThreadKeyedByTheCallersReference(t *testing.T) {
	repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
	svc := newThreadService(repo, &fakeClassifierLLM{})

	res, err := svc.ResolveForAPIUser(context.Background(), "co-1", "their-user-42", "sales last month?")
	if err != nil {
		t.Fatalf("ResolveForAPIUser: %v", err)
	}
	if !res.IsNew {
		t.Error("IsNew = false, want a new thread")
	}
	if res.Thread.Channel != domain.ChannelAPI {
		t.Errorf("channel = %q, want api", res.Thread.Channel)
	}
	if res.Thread.APIUserRef != "their-user-42" {
		t.Errorf("api_user_ref = %q, want the caller's own reference", res.Thread.APIUserRef)
	}
	// Nothing else may be filled in: an api thread that also carried a
	// user_id would be counted twice by the usage rollup's COALESCE.
	if res.Thread.UserID != "" || res.Thread.PhoneNumber != "" ||
		res.Thread.DiscordUserID != "" || res.Thread.LarkOpenID != "" {
		t.Errorf("thread carries another channel's identity: %+v", res.Thread)
	}
}

func TestResolveForAPIUserContinuesAWarmThread(t *testing.T) {
	existing := &domain.ConversationThread{
		ID: "th-1", CompanyID: "co-1", Channel: domain.ChannelAPI,
		APIUserRef: "their-user-42", LastMessageAt: time.Now(),
	}
	repo := &fakeThreadRepo{latest: existing}
	svc := newThreadService(repo, &fakeClassifierLLM{})

	res, err := svc.ResolveForAPIUser(context.Background(), "co-1", "their-user-42", "and the month before?")
	if err != nil {
		t.Fatalf("ResolveForAPIUser: %v", err)
	}
	if res.IsNew || res.Thread.ID != "th-1" {
		t.Errorf("result = %+v, want the existing thread continued", res)
	}
}

func TestResolveForAPIUserRequiresBothIdentifiers(t *testing.T) {
	svc := newThreadService(&fakeThreadRepo{}, &fakeClassifierLLM{})

	if _, err := svc.ResolveForAPIUser(context.Background(), "", "u", "hi"); err == nil {
		t.Error("resolved with no company — a tenant-less lookup is the worst failure mode there is")
	}
	if _, err := svc.ResolveForAPIUser(context.Background(), "co-1", "", "hi"); err == nil {
		t.Error("resolved with no api_user_ref")
	}
}
