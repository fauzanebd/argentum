package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

type fakeActionRepo struct {
	rows []*domain.AgentAction
	err  error
}

func (f *fakeActionRepo) Create(_ context.Context, a *domain.AgentAction) error {
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, a)
	return nil
}

func (f *fakeActionRepo) ListByCompany(context.Context, string, domain.AgentActionFilter) ([]*domain.AgentAction, error) {
	return f.rows, nil
}

// A cron tick is not the person who wrote the schedule. Attributing it to them
// puts a name in the audit log for something they were not present for.
func TestActorOfDistinguishesScheduleFromUser(t *testing.T) {
	cases := []struct {
		name     string
		payload  queue.ChatRunPayload
		wantKind domain.ActorKind
		wantRef  string
	}{
		{
			"scheduled run outranks the author",
			queue.ChatRunPayload{UserID: "user-1", ScheduledTaskID: "task-3"},
			domain.ActorKindSchedule, "task-3",
		},
		{
			// T-13. A turn that arrived over /v1 is attributable to the
			// credential, not to whatever user_ref the tenant put on the
			// request: that string is a label they chose, not an identity we
			// authenticated.
			"api key outranks a caller-supplied user",
			queue.ChatRunPayload{UserID: "user-1", APIKeyID: "key-7"},
			domain.ActorKindAPIKey, "key-7",
		},
		{
			"a schedule still outranks an api key",
			queue.ChatRunPayload{APIKeyID: "key-7", ScheduledTaskID: "task-3"},
			domain.ActorKindSchedule, "task-3",
		},
		{"dashboard user", queue.ChatRunPayload{UserID: "user-1"}, domain.ActorKindUser, "user-1"},
		{"discord", queue.ChatRunPayload{DiscordUserID: "dsc-9"}, domain.ActorKindUser, "dsc-9"},
		{"lark", queue.ChatRunPayload{LarkOpenID: "ou_1"}, domain.ActorKindUser, "ou_1"},
		{"whatsapp", queue.ChatRunPayload{PhoneNumber: "+62811"}, domain.ActorKindUser, "+62811"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, ref := actorOf(tc.payload)
			if domain.ActorKind(kind) != tc.wantKind || ref != tc.wantRef {
				t.Errorf("actorOf = %s/%s, want %s/%s", kind, ref, tc.wantKind, tc.wantRef)
			}
			if !domain.ActorKind(kind).Valid() {
				t.Errorf("actorOf produced %q, which is not a kind", kind)
			}
		})
	}
}

// The ticket's second acceptance item, for the half no tool decorator can see:
// a turn a guardrail stopped never reaches a tool.
func TestRecordBlockedTurnWritesBlockedRow(t *testing.T) {
	repo := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(repo)
	p := queue.ChatRunPayload{
		CompanyID: "co-1",
		ThreadID:  "th-1",
		UserMsgID: "msg-1",
		UserID:    "user-1",
		Channel:   domain.ChannelDashboard,
		Message:   "how do I center a div?",
	}

	r.recordBlockedTurn(context.Background(), p, "guardrail", "off-topic")

	if len(repo.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(repo.rows))
	}
	row := repo.rows[0]
	if row.ResultStatus != domain.ActionStatusBlocked {
		t.Errorf("status = %q, want blocked", row.ResultStatus)
	}
	if row.ToolName != "guardrail" || row.CompanyID != "co-1" || row.ThreadID != "th-1" {
		t.Errorf("row = %+v, want a guardrail row on co-1/th-1", row)
	}
	if row.ActorKind != domain.ActorKindUser || row.ActorRef != "user-1" {
		t.Errorf("actor = %s/%s, want user/user-1", row.ActorKind, row.ActorRef)
	}
	if row.RowsReturned != nil {
		t.Errorf("rows_returned = %v, want nil — nothing ran", *row.RowsReturned)
	}
	// The refused question is the input most likely to hold something the
	// tenant would not want retained; only its fingerprint is stored.
	if len(row.ArgsHash) != 64 {
		t.Errorf("args_hash = %q, want a sha256 hex digest", row.ArgsHash)
	}
	if string(row.ArgsRedacted) != "{}" {
		t.Errorf("args_redacted = %s, want an empty object", row.ArgsRedacted)
	}
}

// The eval harness runs this same runner with no control-plane row to write.
func TestRecordBlockedTurnWithoutRepoIsNoop(t *testing.T) {
	r := &ChatRunner{}
	r.recordBlockedTurn(context.Background(), queue.ChatRunPayload{CompanyID: "co-1"}, "guardrail", "off-topic")
}

func TestRecordBlockedTurnSurvivesRepoError(t *testing.T) {
	r := (&ChatRunner{}).WithActionLog(&fakeActionRepo{err: errors.New("control DB down")})
	r.recordBlockedTurn(context.Background(), queue.ChatRunPayload{CompanyID: "co-1"}, "final_answer", "fabrication")
}
