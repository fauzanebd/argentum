package app

import (
	"context"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// "Cost per answer read aloud", checked by hand: 600 characters × $15 per
// million = $0.009 = 9,000 µUSD on tts-1; twice that on tts-1-hd and on a model
// with no row, which is billed at the highest rate.
func TestRecordSynthesisPricesPerCharacterPerModel(t *testing.T) {
	cases := []struct {
		model string
		want  int64
	}{
		{"tts-1", 9_000},
		{"tts-1-hd", 18_000},
		{"gpt-4o-mini-tts", 9_000},
		{"some-new-voice", 18_000},
	}
	for _, c := range cases {
		repo := &fakeUsageRepo{}
		NewUsageService(repo, &stubCredits{}, DefaultPricing).
			RecordSynthesis(context.Background(), "co-1", "th-1", c.model, 600)
		if len(repo.events) != 1 {
			t.Fatalf("%s: %d events, want 1", c.model, len(repo.events))
		}
		e := repo.events[0]
		if e.EventType != domain.UsageEventSpeechSynthesis || e.Model != c.model || e.ThreadID != "th-1" {
			t.Errorf("%s: event = %+v", c.model, e)
		}
		if e.CostMicroUSD != c.want {
			t.Errorf("%s: cost = %d µUSD, want %d", c.model, e.CostMicroUSD, c.want)
		}
		if e.Metadata["characters"] != 600 {
			t.Errorf("%s: metadata = %v", c.model, e.Metadata)
		}
	}
}

// One character is 15 µUSD and must not round to free; no characters is no call.
func TestRecordSynthesisNeverRecordsAFreeAnswer(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, &stubCredits{}, DefaultPricing)
	svc.RecordSynthesis(context.Background(), "co-1", "th-1", "tts-1", 1)
	svc.RecordSynthesis(context.Background(), "co-1", "th-1", "tts-1", 0)
	if len(repo.events) != 1 || repo.events[0].CostMicroUSD != 15 {
		t.Fatalf("events = %v; want one event at 15 µUSD", repo.events)
	}
}
