package main

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/speech"
)

// productionVoice is this deployment's configuration as its HelmRelease sets
// it: the model on OpenRouter with its key, voice switched on, and no speech
// key or provider of its own — both default to openrouter.
func productionVoice() *config.Config {
	return &config.Config{
		LLMAPIKey:         "sk-or-model",
		LLMBaseURL:        "https://openrouter.ai/api/v1",
		SpeechEnabled:     true,
		SpeechProvider:    "openrouter",
		SpeechTTSProvider: "openrouter",
		SpeechTimeoutSecs: 30,
	}
}

// voice.md §5c: SPEECH_ENABLED is all production sets. Both halves of voice run
// on the model's OpenRouter key.
func TestVoiceRunsOnTheModelsOpenRouterKey(t *testing.T) {
	cfg := productionVoice()
	if !speech.New(transcriberConfig(cfg)).Enabled() {
		t.Error("the transcriber is not usable on the model's OpenRouter key")
	}
	if !speech.NewSynthesizer(synthesizerConfig(cfg)).Enabled() {
		t.Error("the synthesiser is not usable on the model's OpenRouter key")
	}
}

// And the model's key never goes to a host it was not issued for: a model on
// another host, a model with no base URL, and speech pointed somewhere else all
// leave voice without a key — off, and saying so in the log.
func TestVoiceNeverSendsTheModelsKeyToAnotherHost(t *testing.T) {
	for name, change := range map[string]func(*config.Config){
		"the model on OpenAI":   func(c *config.Config) { c.LLMBaseURL = "https://api.openai.com/v1" },
		"the model with no URL": func(c *config.Config) { c.LLMBaseURL = "" },
		"speech pointed at Groq": func(c *config.Config) {
			c.SpeechBaseURL, c.SpeechSTTModel = "https://api.groq.com/openai/v1", "whisper-large-v3-turbo"
			c.SpeechTTSBaseURL, c.SpeechTTSModel, c.SpeechTTSVoice = "https://api.groq.com/openai/v1", "m", "v"
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := productionVoice()
			change(cfg)
			if speech.New(transcriberConfig(cfg)).Enabled() {
				t.Error("the transcriber took the model's key to another host")
			}
			if speech.NewSynthesizer(synthesizerConfig(cfg)).Enabled() {
				t.Error("the synthesiser took the model's key to another host")
			}
		})
	}
}
