package main

import (
	"time"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/speech"
)

// transcriberConfig is voice in's configuration (T-W7), with the model's key
// offered to it as a shared key (voice.md §5).
//
// **Offered, not assigned.** speech.New sends LLM_API_KEY only when
// SPEECH_API_KEY is unset and the transcriber's host is LLM_BASE_URL's: the
// production shape, where both are openrouter.ai, and the only shape in which
// "the same key" is true. It exists because Bitwarden's operator writes a secret
// under the first name mapped to it and ignores a second, so the key cannot be
// mapped to SPEECH_API_KEY as well as LLM_API_KEY.
func transcriberConfig(cfg *config.Config) speech.Config {
	return speech.Config{
		Enabled:          cfg.SpeechEnabled,
		Provider:         cfg.SpeechProvider,
		APIKey:           cfg.SpeechAPIKey,
		SharedKey:        cfg.LLMAPIKey,
		SharedKeyBaseURL: cfg.LLMBaseURL,
		BaseURL:          cfg.SpeechBaseURL,
		Model:            cfg.SpeechSTTModel,
		Timeout:          time.Duration(cfg.SpeechTimeoutSecs) * time.Second,
	}
}

// synthesizerConfig is voice out's (T-W8), on transcriberConfig's terms.
func synthesizerConfig(cfg *config.Config) speech.SynthConfig {
	return speech.SynthConfig{
		Enabled:          cfg.SpeechEnabled,
		Provider:         cfg.SpeechTTSProvider,
		APIKey:           cfg.EffectiveSpeechTTSAPIKey(),
		SharedKey:        cfg.LLMAPIKey,
		SharedKeyBaseURL: cfg.LLMBaseURL,
		BaseURL:          cfg.SpeechTTSBaseURL,
		Model:            cfg.SpeechTTSModel,
		Voice:            cfg.SpeechTTSVoice,
		Timeout:          time.Duration(cfg.SpeechTimeoutSecs) * time.Second,
	}
}
