package config

import (
	"os"
	"strings"
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
	// What Load validates before it answers, none of it about voice. Built rather
	// than written out: the CI secret scan (gitleaks) read the random-looking hex
	// this test first used as a committed JWT secret and DSN key, and failed the
	// build. A run of one character is the right length and says it is not a key.
	t.Setenv("LLM_API_KEY", "test")
	t.Setenv("ARGENTUM_JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("ARGENTUM_DSN_KEY", strings.Repeat("0", 64))
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
