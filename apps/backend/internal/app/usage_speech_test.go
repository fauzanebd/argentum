package app

import (
	"context"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// "Cost per clip into usage_events, priced per second." Checked by hand for the
// three cases: 15 s × $0.04/h = $0.000166…, rounded up to 167 µUSD; 15 s ×
// $0.36/h = $0.0015 = 1,500 µUSD, for whisper-1 and for a model with no row.
func TestRecordTranscriptionPricesPerSecondPerModel(t *testing.T) {
	cases := []struct {
		model string
		want  int64
	}{
		{"whisper-large-v3-turbo", 167},
		{"whisper-1", 1500},
		{"some-new-model", 1500},
	}
	for _, c := range cases {
		repo := &fakeUsageRepo{}
		NewUsageService(repo, &stubCredits{}, DefaultPricing).
			RecordTranscription(context.Background(), "co-1", "th-1", c.model, 15)
		if len(repo.events) != 1 {
			t.Fatalf("%s: %d events, want 1", c.model, len(repo.events))
		}
		e := repo.events[0]
		if e.EventType != domain.UsageEventSpeechTranscription || e.Model != c.model || e.ThreadID != "th-1" {
			t.Errorf("%s: event = %+v", c.model, e)
		}
		if e.CostMicroUSD != c.want {
			t.Errorf("%s: cost = %d µUSD, want %d", c.model, e.CostMicroUSD, c.want)
		}
		if e.Metadata["audio_seconds"] != 15.0 {
			t.Errorf("%s: metadata = %v", c.model, e.Metadata)
		}
	}
}

// A one-second clip on the cheapest model is 11.1 µUSD, and must not round to a
// free call; a clip with no length is not a free call either, it is no call.
func TestRecordTranscriptionNeverRecordsAFreeClip(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, &stubCredits{}, DefaultPricing)
	svc.RecordTranscription(context.Background(), "co-1", "th-1", "whisper-large-v3-turbo", 1)
	svc.RecordTranscription(context.Background(), "co-1", "th-1", "whisper-large-v3-turbo", 0)
	if len(repo.events) != 1 || repo.events[0].CostMicroUSD != 12 {
		t.Fatalf("events = %d, first cost = %v; want one event at 12 µUSD", len(repo.events), repo.events)
	}
}
