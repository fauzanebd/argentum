package config

import (
	"os"
	"testing"
)

// Voice runs on OpenRouter by default (research 08 §2e, the owner's call on
// 2026-09-15): with neither provider named, both halves are "openrouter", and the
// one key a deployment sets reaches the synthesiser too. The variables are unset,
// not set empty, so the test holds whichever way getEnv reads an empty one.
func TestSpeechDefaultsToOneOpenRouterKey(t *testing.T) {
	for _, k := range []string{"SPEECH_PROVIDER", "SPEECH_TTS_PROVIDER", "SPEECH_TTS_API_KEY"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
	t.Setenv("SPEECH_API_KEY", "sk-or-test")
	// What Load validates before it answers, none of it about voice.
	t.Setenv("LLM_API_KEY", "sk-llm-test")
	t.Setenv("ARGENTUM_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ARGENTUM_DSN_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SpeechProvider != "openrouter" || cfg.SpeechTTSProvider != "openrouter" {
		t.Errorf("providers = %q, %q; want openrouter for both", cfg.SpeechProvider, cfg.SpeechTTSProvider)
	}
	if got := cfg.EffectiveSpeechTTSAPIKey(); got != "sk-or-test" {
		t.Errorf("synthesiser key = %q, want the one SPEECH_API_KEY", got)
	}
}
