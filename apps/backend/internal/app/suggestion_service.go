package app

import (
	"context"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SuggestionService records which next-step chips people actually press
// (T-U13), and reports the rate.
//
// The rate is the point. T-Q10 costs one light-model call on every answered turn
// and a row of buttons under every answer, and this is the only evidence either
// is earning its place — the ticket's own rule is that under roughly 5% after a
// real week the feature is cut rather than tuned. A feature whose instrument
// ships later never gets measured, so it ships with the surface that draws it.
type SuggestionService struct {
	picks    domain.SuggestionPickRepository
	messages MessageLookup
}

func NewSuggestionService(picks domain.SuggestionPickRepository, messages MessageLookup) *SuggestionService {
	return &SuggestionService{picks: picks, messages: messages}
}

// PickInput is one click, as it arrives from the browser.
type PickInput struct {
	CompanyID string
	MessageID string
	Index     int
}

// Pick records that a reader chose one of the suggestions under an answer.
//
// **The label and the recommended flag are read off the stored message, never
// taken from the request.** The client sends only an index. Trusting a browser
// for the other two would let a bad client — or a stale tab rendering a message
// that has since changed — write a row saying the reader pressed something that
// was never on screen, and this table's whole value is that it says what
// actually happened.
func (s *SuggestionService) Pick(ctx context.Context, in PickInput) (*domain.SuggestionPick, error) {
	msg, err := s.messages.GetForCompany(ctx, in.CompanyID, in.MessageID)
	if err != nil {
		return nil, err
	}
	steps := NextStepsOf(msg)
	if in.Index < 0 || in.Index >= len(steps) {
		// Refused rather than recorded with an empty label: an index outside the
		// suggestions on the message is a client bug or a stale tab, and a row
		// that says somebody pressed a chip that was not there is worse than no
		// row. Same argument ErrNotAssistantMessage makes for a rating aimed at
		// the wrong id.
		return nil, fmt.Errorf("%w: this message has %d suggestions, so index %d names none of them",
			domain.ErrInvalidInput, len(steps), in.Index)
	}
	step := steps[in.Index]

	p := &domain.SuggestionPick{
		CompanyID:   in.CompanyID,
		MessageID:   in.MessageID,
		Index:       in.Index,
		Recommended: step.Recommended,
		Label:       step.Label,
	}
	p.Normalize()
	if err := s.picks.Append(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Summary is the pick rate over a window.
func (s *SuggestionService) Summary(ctx context.Context, companyID string, from, to time.Time) (*domain.SuggestionPickSummary, error) {
	return s.picks.SummaryByCompany(ctx, companyID, from, to)
}

// NextStepsOf reads the suggestions off a message's metadata.
//
// The metadata column is `map[string]any` on both sides of the wire, so this is
// the one place that knows the shape — and it answers an empty slice for
// everything unexpected rather than an error. A malformed blob must not make an
// answer unreadable: the failure it would otherwise cause is a transcript that
// will not render because of a field nothing in the transcript depends on.
func NextStepsOf(m *domain.Message) []domain.NextStep {
	if m == nil || m.Metadata == nil {
		return nil
	}
	raw, ok := m.Metadata["next_steps"].([]any)
	if !ok {
		return nil
	}
	out := make([]domain.NextStep, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		label, _ := obj["label"].(string)
		prompt, _ := obj["prompt"].(string)
		if label == "" || prompt == "" {
			continue
		}
		recommended, _ := obj["recommended"].(bool)
		why, _ := obj["why"].(string)
		out = append(out, domain.NextStep{
			Label: label, Prompt: prompt, Recommended: recommended, Why: why,
		})
	}
	return out
}
