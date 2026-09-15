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
			RecordTranscription(context.Background(), "co-1", "th-1", c.model, 15, 0)
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

// A one-second clip on the cheapest model is billed as Groq's ten, 111.1 µUSD,
// and must round up rather than down; a clip with no length is not a free call
// either, it is no call.
func TestRecordTranscriptionNeverRecordsAFreeClip(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, &stubCredits{}, DefaultPricing)
	svc.RecordTranscription(context.Background(), "co-1", "th-1", "whisper-large-v3-turbo", 1, 0)
	svc.RecordTranscription(context.Background(), "co-1", "th-1", "whisper-large-v3-turbo", 0, 0)
	if len(repo.events) != 1 || repo.events[0].CostMicroUSD != 112 {
		t.Fatalf("events = %d, first cost = %v; want one event at 112 µUSD", len(repo.events), repo.events)
	}
}

// OpenRouter says what it charged (research 08 §2e), and that charge is the row:
// not the rate, and not a minimum. By hand: $0.0000075 is 7.5 µUSD, rounded up
// to 8. The same clip with no charge reported falls to the OpenRouter row, the
// dearer of turbo's two hosts: 2.75 s × $0.04/h = 30.6 µUSD, up to 31, with no
// Groq minimum, because which host heard it is unknown.
func TestRecordTranscriptionRecordsWhatTheProviderCharged(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, &stubCredits{}, DefaultPricing)
	svc.RecordTranscription(context.Background(), "co-1", "th-1", "openai/whisper-large-v3-turbo", 2.75, 0.0000075)
	svc.RecordTranscription(context.Background(), "co-1", "th-1", "openai/whisper-large-v3-turbo", 2.75, 0)
	if len(repo.events) != 2 {
		t.Fatalf("%d events, want 2", len(repo.events))
	}
	charged, priced := repo.events[0], repo.events[1]
	if charged.CostMicroUSD != 8 || charged.Metadata["cost_source"] != "provider" || charged.Metadata["audio_seconds"] != 2.75 {
		t.Errorf("reported charge recorded as %d µUSD, metadata %v; want 8 from the provider", charged.CostMicroUSD, charged.Metadata)
	}
	if priced.CostMicroUSD != 31 || priced.Metadata["cost_source"] != nil || priced.Metadata["billed_seconds"] != nil {
		t.Errorf("no charge reported: %d µUSD, metadata %v; want 31 at the row's rate, no minimum", priced.CostMicroUSD, priced.Metadata)
	}
}

// Groq bills every clip as at least 10 seconds (console.groq.com/docs/speech-to-text,
// read 2026-09-15), so the ledger does too, or a short question reads cheaper in
// usage_events than on the invoice. By hand: 2.75 s on turbo is 10 × $0.04/h =
// 111.1 µUSD, up to 112 — voice.md §1f's clip, recorded at 31 before this. On
// whisper-large-v3, 10 × $0.111/h = 308.3, up to 309. A clip over the minimum is
// billed as measured, and whisper-1 has no minimum. The length heard stays in
// audio_seconds; what was billed is beside it only when the two differ.
func TestRecordTranscriptionBillsTheProvidersMinimumLength(t *testing.T) {
	cases := []struct {
		model   string
		seconds float64
		want    int64
		billed  interface{}
	}{
		{"whisper-large-v3-turbo", 2.75, 112, 10.0},
		{"whisper-large-v3", 2.75, 309, 10.0},
		{"whisper-large-v3-turbo", 15, 167, nil},
		{"whisper-1", 5, 500, nil},
	}
	for _, c := range cases {
		repo := &fakeUsageRepo{}
		NewUsageService(repo, &stubCredits{}, DefaultPricing).
			RecordTranscription(context.Background(), "co-1", "th-1", c.model, c.seconds, 0)
		if len(repo.events) != 1 {
			t.Fatalf("%s %.2fs: %d events, want 1", c.model, c.seconds, len(repo.events))
		}
		e := repo.events[0]
		if e.CostMicroUSD != c.want {
			t.Errorf("%s %.2fs: cost = %d µUSD, want %d", c.model, c.seconds, e.CostMicroUSD, c.want)
		}
		if e.Metadata["audio_seconds"] != c.seconds {
			t.Errorf("%s %.2fs: audio_seconds = %v, want the length heard", c.model, c.seconds, e.Metadata["audio_seconds"])
		}
		if e.Metadata["billed_seconds"] != c.billed {
			t.Errorf("%s %.2fs: billed_seconds = %v, want %v", c.model, c.seconds, e.Metadata["billed_seconds"], c.billed)
		}
	}
}
