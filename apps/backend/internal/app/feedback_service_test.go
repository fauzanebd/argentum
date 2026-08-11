package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type fbMessages struct {
	msg *domain.Message
	err error
}

func (m fbMessages) GetForCompany(_ context.Context, _, _ string) (*domain.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.msg, nil
}

type fbRepo struct {
	saved   []*domain.MessageFeedback
	upsertE error
}

func (r *fbRepo) Upsert(_ context.Context, f *domain.MessageFeedback) error {
	if r.upsertE != nil {
		return r.upsertE
	}
	f.ID = "fb-1"
	r.saved = append(r.saved, f)
	return nil
}

func (r *fbRepo) GetByMessage(context.Context, string, string) ([]*domain.MessageFeedback, error) {
	return r.saved, nil
}

func (r *fbRepo) ListByCompany(context.Context, string, bool, int, int) ([]*domain.MessageFeedback, error) {
	return r.saved, nil
}

func (r *fbRepo) Summarize(context.Context, string, time.Time, time.Time) (domain.FeedbackSummary, error) {
	return domain.FeedbackSummary{Rated: len(r.saved)}, nil
}

func (r *fbRepo) NegativeMessageIDs(context.Context, string, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func assistantMsg() *domain.Message {
	return &domain.Message{ID: "msg-1", ThreadID: "thr-1", Role: domain.MessageRoleAssistant}
}

func TestRateStoresTheVerdict(t *testing.T) {
	repo := &fbRepo{}
	svc := NewFeedbackService(repo, fbMessages{msg: assistantMsg()})

	got, err := svc.Rate(context.Background(), RateInput{
		CompanyID: "co-1", MessageID: "msg-1", Rating: domain.FeedbackDown,
		Reason: "counted line items, not orders", ActorKind: domain.ActorKindUser, ActorRef: "u-1",
	})
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if got.Rating != domain.FeedbackDown {
		t.Errorf("rating = %d, want -1", got.Rating)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved %d rows, want 1", len(repo.saved))
	}
}

// The thread comes off the message, never off the caller. A client that could
// name the thread could file a verdict about message A against thread B, and
// every later read — the tuning list, T-Q8's join — would believe it.
func TestRateTakesThreadFromTheMessageNotTheCaller(t *testing.T) {
	repo := &fbRepo{}
	msg := assistantMsg()
	msg.ThreadID = "the-real-thread"
	svc := NewFeedbackService(repo, fbMessages{msg: msg})

	got, err := svc.Rate(context.Background(), RateInput{
		CompanyID: "co-1", MessageID: "msg-1", Rating: domain.FeedbackUp,
		ActorKind: domain.ActorKindUser, ActorRef: "u-1",
	})
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if got.ThreadID != "the-real-thread" {
		t.Errorf("thread_id = %q, want the message's own", got.ThreadID)
	}
}

// Refused rather than accepted-and-ignored: a client sending the wrong id
// would otherwise get a 200 and believe the rating landed.
func TestRateRefusesNonAssistantMessages(t *testing.T) {
	for _, role := range []domain.MessageRole{
		domain.MessageRoleUser, domain.MessageRoleTool, domain.MessageRoleSystem,
	} {
		msg := assistantMsg()
		msg.Role = role
		svc := NewFeedbackService(&fbRepo{}, fbMessages{msg: msg})
		_, err := svc.Rate(context.Background(), RateInput{
			CompanyID: "co-1", MessageID: "msg-1", Rating: domain.FeedbackUp,
			ActorKind: domain.ActorKindUser,
		})
		if !errors.Is(err, ErrNotAssistantMessage) {
			t.Errorf("role %q: err = %v, want ErrNotAssistantMessage", role, err)
		}
	}
}

func TestRateRejectsBadInput(t *testing.T) {
	svc := NewFeedbackService(&fbRepo{}, fbMessages{msg: assistantMsg()})
	tests := []struct {
		name string
		in   RateInput
	}{
		{"rating zero", RateInput{CompanyID: "co", MessageID: "m", ActorKind: domain.ActorKindUser}},
		{"rating out of range", RateInput{CompanyID: "co", MessageID: "m", Rating: 5, ActorKind: domain.ActorKindUser}},
		{"no company", RateInput{MessageID: "m", Rating: 1, ActorKind: domain.ActorKindUser}},
		{"no message", RateInput{CompanyID: "co", Rating: 1, ActorKind: domain.ActorKindUser}},
		{"unknown actor", RateInput{CompanyID: "co", MessageID: "m", Rating: 1, ActorKind: "ghost"}},
	}
	for _, tt := range tests {
		if _, err := svc.Rate(context.Background(), tt.in); err == nil {
			t.Errorf("%s: Rate accepted invalid input", tt.name)
		}
	}
}

// A tenant's message id that belongs to somebody else comes back as
// ErrNotFound from the scoped read, and must travel out unchanged — the
// handler turns it into a 404 rather than a 403 so a bare uuid cannot be used
// to confirm a row exists.
func TestRatePropagatesNotFound(t *testing.T) {
	svc := NewFeedbackService(&fbRepo{}, fbMessages{err: domain.ErrNotFound})
	_, err := svc.Rate(context.Background(), RateInput{
		CompanyID: "co-1", MessageID: "someone-elses", Rating: domain.FeedbackUp,
		ActorKind: domain.ActorKindUser,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestReasonIsCappedNotRejected(t *testing.T) {
	repo := &fbRepo{}
	svc := NewFeedbackService(repo, fbMessages{msg: assistantMsg()})

	long := strings.Repeat("x", domain.FeedbackReasonMaxChars+500)
	got, err := svc.Rate(context.Background(), RateInput{
		CompanyID: "co-1", MessageID: "msg-1", Rating: domain.FeedbackDown, Reason: long,
		ActorKind: domain.ActorKindUser,
	})
	if err != nil {
		t.Fatalf("an over-long reason must be capped, not refused: %v", err)
	}
	if len(got.Reason) != domain.FeedbackReasonMaxChars {
		t.Errorf("reason kept %d chars, want the cap %d", len(got.Reason), domain.FeedbackReasonMaxChars)
	}
}

// "No reported failures" and "nobody looked" are different claims. Rated is
// what tells them apart, so DownRate must not invent a rate for an empty
// window.
func TestDownRateOfAnEmptyWindowIsZeroNotUndefined(t *testing.T) {
	if got := (domain.FeedbackSummary{}).DownRate(); got != 0 {
		t.Errorf("DownRate() = %v on an empty window, want 0", got)
	}
	s := domain.FeedbackSummary{Rated: 4, Up: 3, Down: 1}
	if got := s.DownRate(); got != 0.25 {
		t.Errorf("DownRate() = %v, want 0.25", got)
	}
}

func TestSummaryRejectsInvertedWindow(t *testing.T) {
	svc := NewFeedbackService(&fbRepo{}, fbMessages{msg: assistantMsg()})
	now := time.Now()
	if _, err := svc.Summary(context.Background(), "co-1", now, now.Add(-time.Hour)); err == nil {
		t.Error("Summary accepted a window that ends before it starts")
	}
}
