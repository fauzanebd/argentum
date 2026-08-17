package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type spMessages struct {
	msg *domain.Message
	err error
}

func (m spMessages) GetForCompany(_ context.Context, _, _ string) (*domain.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.msg, nil
}

type spPicks struct {
	got []*domain.SuggestionPick
	err error
	sum *domain.SuggestionPickSummary
}

func (p *spPicks) Append(_ context.Context, in *domain.SuggestionPick) error {
	if p.err != nil {
		return p.err
	}
	in.ID = "pick-1"
	p.got = append(p.got, in)
	return nil
}

func (p *spPicks) SummaryByCompany(context.Context, string, time.Time, time.Time) (*domain.SuggestionPickSummary, error) {
	return p.sum, nil
}

// A message carrying the chips T-Q10 wrote, as they come back off the wire:
// metadata is map[string]any, so the suggestions are []any of map[string]any and
// not []domain.NextStep.
func messageWithSteps() *domain.Message {
	return &domain.Message{
		ID:   "msg-1",
		Role: domain.MessageRoleAssistant,
		Metadata: map[string]any{
			"next_steps": []any{
				map[string]any{"label": "By region", "prompt": "Break that down by region"},
				map[string]any{
					"label": "Compare with last year", "prompt": "How does that compare with last year?",
					"recommended": true, "why": "the total hides the direction",
				},
			},
		},
	}
}

// The property the whole table's value rests on: what was on the button is read
// off the stored message, never taken from the request. A client sends an index
// and nothing else, so a stale tab cannot write a row saying somebody pressed a
// chip that was never on screen.
func TestPickReadsTheLabelOffTheMessage(t *testing.T) {
	picks := &spPicks{}
	svc := NewSuggestionService(picks, spMessages{msg: messageWithSteps()})

	got, err := svc.Pick(t.Context(), PickInput{CompanyID: "co-1", MessageID: "msg-1", Index: 1})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got.Label != "Compare with last year" || !got.Recommended {
		t.Errorf("pick = %+v", got)
	}
	if len(picks.got) != 1 || picks.got[0].Index != 1 {
		t.Errorf("stored = %+v", picks.got)
	}
}

func TestPickRecordsANonRecommendedChoice(t *testing.T) {
	picks := &spPicks{}
	svc := NewSuggestionService(picks, spMessages{msg: messageWithSteps()})

	got, err := svc.Pick(t.Context(), PickInput{CompanyID: "co-1", MessageID: "msg-1", Index: 0})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got.Recommended {
		t.Errorf("pick = %+v; index 0 is not the marked one", got)
	}
}

// An index outside the message's suggestions is a client bug or a stale tab. A
// row saying somebody pressed a chip that was not there is worse than no row.
func TestPickRefusesAnIndexTheMessageDoesNotHave(t *testing.T) {
	picks := &spPicks{}
	svc := NewSuggestionService(picks, spMessages{msg: messageWithSteps()})

	for _, idx := range []int{-1, 2, 99} {
		if _, err := svc.Pick(t.Context(), PickInput{CompanyID: "co-1", MessageID: "msg-1", Index: idx}); err == nil {
			t.Errorf("index %d was accepted", idx)
		} else if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("index %d: err = %v, want ErrInvalidInput so the handler answers 400", idx, err)
		}
	}
	if len(picks.got) != 0 {
		t.Errorf("stored %d rows for refused picks", len(picks.got))
	}
}

func TestPickOnAMessageWithNoSuggestionsIsRefused(t *testing.T) {
	picks := &spPicks{}
	svc := NewSuggestionService(picks, spMessages{msg: &domain.Message{ID: "msg-1"}})

	if _, err := svc.Pick(t.Context(), PickInput{CompanyID: "co-1", MessageID: "msg-1"}); err == nil {
		t.Fatal("a pick on a message with no chips was accepted")
	}
}

// A malformed metadata blob renders nothing rather than blanking a transcript.
// The failure it would otherwise cause is a conversation that will not draw
// because of a field nothing in the conversation depends on.
func TestNextStepsOfToleratesRubbish(t *testing.T) {
	cases := []*domain.Message{
		nil,
		{},
		{Metadata: map[string]any{}},
		{Metadata: map[string]any{"next_steps": "not a list"}},
		{Metadata: map[string]any{"next_steps": []any{"not an object", 42, nil}}},
		{Metadata: map[string]any{"next_steps": []any{map[string]any{"label": "no prompt"}}}},
		{Metadata: map[string]any{"next_steps": []any{map[string]any{"prompt": "no label"}}}},
	}
	for i, m := range cases {
		if got := NextStepsOf(m); len(got) != 0 {
			t.Errorf("case %d: NextStepsOf = %+v, want none", i, got)
		}
	}
}

// The rate is over answers that OFFERED a suggestion, not over turns: a turn
// that asked a clarifying question, a report turn and a watcher briefing all
// deliberately carry none, and counting them would report a feature as ignored
// when it was never shown.
func TestPickRateIsOverAnswersThatOfferedOne(t *testing.T) {
	s := domain.SuggestionPickSummary{Offered: 40, Picked: 6, Picks: 9}
	if got := s.PickRate(); got != 0.15 {
		t.Errorf("PickRate = %v, want 0.15", got)
	}
	// Picks, not Picked, is the raw event count — two clicks on one answer is
	// one answer acted on, and a rate whose numerator counted clicks could
	// exceed 1.
	if (domain.SuggestionPickSummary{Offered: 0}).PickRate() != 0 {
		t.Error("PickRate divided by a zero denominator")
	}
}

func TestSuggestionLabelIsCapped(t *testing.T) {
	p := &domain.SuggestionPick{Label: "  " + string(make([]rune, 200)) + "  "}
	p.Normalize()
	if len([]rune(p.Label)) > domain.SuggestionLabelMaxChars {
		t.Errorf("label is %d runes", len([]rune(p.Label)))
	}
}
