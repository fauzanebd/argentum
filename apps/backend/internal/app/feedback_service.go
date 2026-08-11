package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// MessageLookup is the half of the message store this service needs: one
// message, with the tenant boundary inside the query.
//
// Declared at the consumer rather than added to domain.MessageRepository, for
// the reason ChatRunner's own narrow interfaces give: the shared interface is
// implemented by six test stubs across three packages, and widening it to add
// a method one service calls makes every one of them carry a function nothing
// asks them. *postgres.MessageRepo satisfies it.
type MessageLookup interface {
	GetForCompany(ctx context.Context, companyID, id string) (*domain.Message, error)
}

// FeedbackService records what people thought of an answer (T-Q2).
//
// It is the first thing in this product that measures the agent against
// reality rather than against the golden set. Forty synthetic questions on one
// demo schema was the whole of the quality signal until this existed, and the
// baseline says so in its own words: *"it is not a claim that the agent is
// 96.8% correct"*.
type FeedbackService struct {
	repo     domain.MessageFeedbackRepository
	messages MessageLookup
}

func NewFeedbackService(repo domain.MessageFeedbackRepository, messages MessageLookup) *FeedbackService {
	return &FeedbackService{repo: repo, messages: messages}
}

// ErrNotAssistantMessage is returned for a rating aimed at a user's own
// message, or at a tool row.
//
// Refused rather than accepted-and-ignored because the id came from a request
// body: a client sending the wrong id would otherwise get a 200 and believe
// the rating landed, and the row it wanted rated would stay unrated forever.
var ErrNotAssistantMessage = errors.New("only an assistant message can be rated")

// RateInput is one verdict, as it arrives from any door.
type RateInput struct {
	CompanyID string
	MessageID string
	Rating    domain.FeedbackRating
	Reason    string
	ActorKind domain.ActorKind
	ActorRef  string
}

// Rate records a verdict, replacing this actor's previous one on the same
// message.
//
// The message is read first, and not only to validate: the thread id on the
// stored row comes from the message rather than from the caller. A client that
// could name the thread could file a verdict about message A against thread B,
// and every read of this table afterwards — the tuning list, T-Q8's join —
// would be reading a lie it has no way to detect.
func (s *FeedbackService) Rate(ctx context.Context, in RateInput) (*domain.MessageFeedback, error) {
	if !in.Rating.Valid() {
		return nil, fmt.Errorf("rating must be 1 or -1, got %d", in.Rating)
	}
	if in.CompanyID == "" || in.MessageID == "" {
		return nil, fmt.Errorf("company and message are required")
	}
	if !in.ActorKind.Valid() {
		return nil, fmt.Errorf("unknown actor kind %q", in.ActorKind)
	}

	msg, err := s.messages.GetForCompany(ctx, in.CompanyID, in.MessageID)
	if err != nil {
		return nil, err
	}
	if msg.Role != domain.MessageRoleAssistant {
		return nil, ErrNotAssistantMessage
	}

	f := &domain.MessageFeedback{
		CompanyID: in.CompanyID,
		ThreadID:  msg.ThreadID,
		MessageID: msg.ID,
		Rating:    in.Rating,
		Reason:    in.Reason,
		ActorKind: in.ActorKind,
		ActorRef:  in.ActorRef,
	}
	if truncated := f.Normalize(); truncated {
		logrus.WithFields(logrus.Fields{
			"company_id": in.CompanyID,
			"message_id": in.MessageID,
			"cap_chars":  domain.FeedbackReasonMaxChars,
		}).Info("feedback reason truncated to the cap")
	}
	if err := s.repo.Upsert(ctx, f); err != nil {
		return nil, err
	}

	// A wrong answer is the event this table exists to catch, so it is logged
	// at warn where an approval is not logged at all. This is the line an
	// operator greps when a tenant says "it has been getting things wrong" —
	// before this ticket there was nothing to grep.
	if f.Rating == domain.FeedbackDown {
		logrus.WithFields(logrus.Fields{
			"company_id": f.CompanyID,
			"thread_id":  f.ThreadID,
			"message_id": f.MessageID,
			"actor_kind": string(f.ActorKind),
			"reason":     f.Reason,
		}).Warn("an answer was marked wrong by the person who read it")
	}
	return f, nil
}

// ForMessage returns every verdict on one message.
func (s *FeedbackService) ForMessage(ctx context.Context, companyID, messageID string) ([]*domain.MessageFeedback, error) {
	return s.repo.GetByMessage(ctx, companyID, messageID)
}

// Recent is the tuning list: the newest verdicts for a tenant, optionally only
// the ones that reported a failure.
func (s *FeedbackService) Recent(ctx context.Context, companyID string, onlyNegative bool, limit, offset int) ([]*domain.MessageFeedback, error) {
	return s.repo.ListByCompany(ctx, companyID, onlyNegative, limit, offset)
}

// Summary rolls up a window. An empty window is not an error and not a gap: it
// means nobody rated anything, which FeedbackSummary.Rated says plainly.
func (s *FeedbackService) Summary(ctx context.Context, companyID string, from, to time.Time) (domain.FeedbackSummary, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.AddDate(0, 0, -30)
	}
	if !from.Before(to) {
		return domain.FeedbackSummary{}, fmt.Errorf("from must be before to")
	}
	return s.repo.Summarize(ctx, companyID, from, to)
}
