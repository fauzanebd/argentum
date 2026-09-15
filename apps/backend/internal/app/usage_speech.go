package app

import (
	"context"
	"math"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// speechPricing is USD per hour of audio, per transcription model (T-W7).
//
// Per hour because that is how both providers publish it, and per model rather
// than one figure on Pricing because the two defaults differ ninefold: the
// provider is meant to be chosen on Indonesian accuracy (research 08 §2a), and a
// single rate would mis-bill whichever one that measurement picks.
var speechPricing = map[string]float64{
	// Groq — research 08 §2a; v3 read from Groq's speech-to-text page 2026-09-15.
	"whisper-large-v3-turbo": 0.04,
	"whisper-large-v3":       0.111,
	// OpenAI — $0.006 a minute.
	"whisper-1": 0.36,
}

// speechMinimumSeconds is the shortest length a provider bills a clip as, per
// model. Groq bills every clip as at least ten seconds, and most spoken
// questions are shorter, so without it the ledger under-records nearly every
// clip on the default provider (research 08 §2e). Keyed by model, like
// speechPricing, whose rates already assume these names are Groq's.
var speechMinimumSeconds = map[string]float64{
	"whisper-large-v3-turbo": 10,
	"whisper-large-v3":       10,
}

// speechFallbackPerHour is what an unpriced model is billed at: the highest
// rate above, for DefaultPricing's reason. An unpriced call that reads as cheap
// is one nobody investigates; one that reads as expensive gets a row added.
const speechFallbackPerHour = 0.36

func speechCostPerSecond(model string) float64 {
	if rate, ok := speechPricing[strings.ToLower(strings.TrimSpace(model))]; ok {
		return rate / 3600
	}
	return speechFallbackPerHour / 3600
}

// RecordTranscription records seconds of audio sent to a speech provider.
//
// Billed on the longer of the clip and the model's minimum. The length heard
// stays in audio_seconds; billed_seconds is added only when the minimum raised
// it, so a clip that was billed as measured reads exactly as before.
//
// Rounded up to a whole micro-dollar. A fifteen-second clip on the cheapest
// model is 167 µUSD, and rounding down would make a short clip on a model with
// no minimum free — a metered call at zero cost is invisible in every summary
// sorted by spend. A clip that reported no length records nothing,
// RecordRenderSeconds' rule.
func (s *UsageService) RecordTranscription(ctx context.Context, companyID, threadID, model string, seconds float64) {
	if seconds <= 0 {
		return
	}
	billed := seconds
	metadata := map[string]interface{}{"audio_seconds": seconds}
	if minimum := speechMinimumSeconds[strings.ToLower(strings.TrimSpace(model))]; seconds < minimum {
		billed = minimum
		metadata["billed_seconds"] = minimum
	}
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventSpeechTranscription,
		Model:        model,
		CostMicroUSD: int64(math.Ceil(billed * speechCostPerSecond(model) * 1_000_000)),
		Metadata:     metadata,
	})
}

// synthesisPricing is USD per million characters read aloud, per synthesis
// model (T-W8), from OpenAI's published prices.
//
// `gpt-4o-mini-tts` is not really priced this way. It is billed per audio token
// produced, and `/audio/speech` answers with the audio and no usage, so nothing
// here can count those tokens. Its row is OpenAI's own conversion of that price
// to characters, an estimate, and the reason speech.voiceProviders defaults to
// `tts-1`, whose price per character is the price.
var synthesisPricing = map[string]float64{
	"tts-1":           15,
	"tts-1-hd":        30,
	"gpt-4o-mini-tts": 15,
}

// synthesisFallbackPerMillion is an unpriced model's rate: the highest above,
// for speechFallbackPerHour's reason.
const synthesisFallbackPerMillion = 30.0

// RecordSynthesis records characters of text sent to a speech synthesiser.
//
// A rate per million characters in USD is the same number in µUSD per
// character, so the cost is characters times the rate, rounded up for
// RecordTranscription's reason. No text records nothing.
func (s *UsageService) RecordSynthesis(ctx context.Context, companyID, threadID, model string, chars int) {
	if chars <= 0 {
		return
	}
	rate := synthesisFallbackPerMillion
	if r, ok := synthesisPricing[strings.ToLower(strings.TrimSpace(model))]; ok {
		rate = r
	}
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventSpeechSynthesis,
		Model:        model,
		CostMicroUSD: int64(math.Ceil(float64(chars) * rate)),
		Metadata:     map[string]interface{}{"characters": chars},
	})
}
