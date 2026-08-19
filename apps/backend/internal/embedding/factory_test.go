package embedding

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/config"
)

// TestEnvKeyResolves pins the question a boot log has to answer honestly: does
// this process's environment supply an embedding credential at all? Every
// `false` below is a deployment where the table picker, the cookbook and the
// dense half of document search do nothing — and where, until 2026-08-19, the
// only line about it said "enabled".
func TestEnvKeyResolves(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "disabled outright",
			cfg:  config.Config{EmbeddingEnabled: false, EmbeddingAPIKey: "sk-real"},
		},
		{
			name: "enabled with no key anywhere",
			cfg:  config.Config{EmbeddingEnabled: true},
		},
		{
			name: "enabled with its own key",
			cfg:  config.Config{EmbeddingEnabled: true, EmbeddingAPIKey: "sk-real"},
			want: true,
		},
		{
			name: "borrowing the primary key on the same host",
			cfg: config.Config{
				EmbeddingEnabled: true,
				LLMInterface:     "openai", LLMAPIKey: "sk-real",
			},
			want: true,
		},
		{
			// The 2026-08-14 finding: an OpenRouter key must not be borrowed for
			// api.openai.com. No key resolves, and the log has to say so.
			name: "the primary key belongs to a different host",
			cfg: config.Config{
				EmbeddingEnabled: true,
				LLMInterface:     "openai", LLMAPIKey: "sk-or-v1-real",
				LLMBaseURL: "https://openrouter.ai/api/v1",
			},
		},
		{
			name: "an unsupported provider",
			cfg: config.Config{
				EmbeddingEnabled: true, EmbeddingAPIKey: "sk-real",
				EmbeddingProvider: "cohere",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			if got := EnvKeyResolves(&cfg); got != tc.want {
				t.Errorf("EnvKeyResolves() = %v, want %v", got, tc.want)
			}
			// The log must never panic on a config this coarse; it runs at boot
			// before anything else is up.
			LogEnvCoverage(&cfg)
		})
	}
}
