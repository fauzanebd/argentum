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
	// OpenRouter — used only when its answer carries no `usage.cost`, which is
	// billed instead. Its turbo has two hosts, DeepInfra (≈$0.012 an hour) and
	// Groq ($0.04), and which one heard a clip is OpenRouter's to decide, so the
	// row is the dearer of the two (research 08 §2e).
	"openai/whisper-large-v3-turbo": 0.04,
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

// RecordTranscription records seconds of audio sent to a speech provider, and
// what the provider charged for them when it said so (providerCostUSD > 0).
//
// **A charge the provider reports is recorded as it is**, ahead of the rate and
// the minimum below: it is the invoice line, not this table's reconstruction of
// it. OpenRouter reports one (speech.Transcript.CostUSD); Groq and OpenAI do
// not, and are priced here. `cost_source` says which a row is.
//
// Otherwise billed on the longer of the clip and the model's minimum. The length
// heard stays in audio_seconds; billed_seconds is added only when the minimum
// raised it, so a clip that was billed as measured reads exactly as before.
//
// Rounded up to a whole micro-dollar. A fifteen-second clip on the cheapest
// model is 167 µUSD, and rounding down would make a short clip on a model with
// no minimum free — a metered call at zero cost is invisible in every summary
// sorted by spend. A clip that reported no length and no charge records nothing,
// RecordRenderSeconds' rule.
func (s *UsageService) RecordTranscription(ctx context.Context, companyID, threadID, model string, seconds, providerCostUSD float64) {
	if providerCostUSD > 0 {
		s.append(ctx, &domain.UsageEvent{
			CompanyID:    companyID,
			ThreadID:     threadID,
			EventType:    domain.UsageEventSpeechTranscription,
			Model:        model,
			CostMicroUSD: int64(math.Ceil(providerCostUSD * 1_000_000)),
			Metadata:     map[string]interface{}{"audio_seconds": seconds, "cost_source": "provider"},
		})
		return
	}
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
	// OpenRouter's default voice (research 08 §2e) — a ceiling, not a price.
	// Google bills it $20 per million audio tokens at 25 tokens a second of
	// speech, which is $500 per million seconds, plus $1 per million text tokens,
	// under a dollar per million characters. Per character, that depends on how
	// fast the voice reads, and nobody here has measured it. At ten characters a
	// second — a slow reading pace — it is $50 per million; a faster voice costs
	// less. So the row over-records rather than under, until live-gate §7n sets it
	// against OpenRouter's own activity page.
	"google/gemini-3.1-flash-tts-preview": 50,
}

// synthesisFallbackPerMillion is an unpriced model's rate, for
// speechFallbackPerHour's reason: tts-1-hd's, the dearest price per character
// anybody has published above. The Gemini row is higher, but it is a ceiling of
// this file's own making rather than a price, and an unknown voice is not
// assumed to cost what a guess about one voice's reading speed does.
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
