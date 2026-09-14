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
	// Groq — research 08 §2a.
	"whisper-large-v3-turbo": 0.04,
	// OpenAI — $0.006 a minute.
	"whisper-1": 0.36,
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
// Rounded up to a whole micro-dollar. A fifteen-second clip on the cheapest
// model is 167 µUSD, and rounding down would make a one-second clip free — a
// metered call at zero cost is invisible in every summary sorted by spend.
// A clip that reported no length records nothing, RecordRenderSeconds' rule.
func (s *UsageService) RecordTranscription(ctx context.Context, companyID, threadID, model string, seconds float64) {
	if seconds <= 0 {
		return
	}
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventSpeechTranscription,
		Model:        model,
		CostMicroUSD: int64(math.Ceil(seconds * speechCostPerSecond(model) * 1_000_000)),
		Metadata:     map[string]interface{}{"audio_seconds": seconds},
	})
}
