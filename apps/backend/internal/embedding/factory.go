package embedding

import (
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/config"
)

// ProfileSpec is the fully-resolved spec needed to build an embedding client.
// Used by both the env-default Build below and the per-tenant resolver in
// internal/llmtenant.
type ProfileSpec struct {
	Provider  string // currently only "openai"
	APIKey    string
	BaseURL   string
	Model     string
	Dim       int
	BatchSize int
}

// BuildForProfile constructs an embedding client from a resolved spec.
// Returns nil (no error) when the provider is unsupported or the API key
// is empty — callers branch on nil to silent-skip embedding work.
func BuildForProfile(spec ProfileSpec) Client {
	provider := strings.ToLower(strings.TrimSpace(spec.Provider))
	if provider == "" {
		provider = "openai"
	}
	if provider != "openai" {
		return nil
	}
	if strings.TrimSpace(spec.APIKey) == "" {
		return nil
	}
	return NewOpenAI(spec.APIKey, spec.BaseURL, spec.Model, spec.Dim, spec.BatchSize)
}

// Build returns an embedding Client when the feature is enabled and a key
// is available; otherwise returns nil so callers can branch on that. We
// don't fatal: per-source toggle still drives the behaviour and a missing
// embedding client just means the table-picker hint never injects.
func Build(cfg *config.Config) Client {
	if !cfg.EmbeddingEnabled {
		return nil
	}
	apiKey := cfg.EffectiveEmbeddingAPIKey()
	if apiKey == "" {
		logrus.Warn("embedding: EMBEDDING_API_KEY not set, and the primary LLM key cannot be borrowed — it is either not an OpenAI-interface key or it belongs to a different host than EMBEDDING_BASE_URL. Table picker and cookbook retrieval are disabled; set EMBEDDING_API_KEY to enable them")
		return nil
	}
	if cfg.EmbeddingProvider != "" && cfg.EmbeddingProvider != "openai" {
		logrus.WithField("provider", cfg.EmbeddingProvider).
			Warn("embedding: only 'openai' provider is supported; table picker disabled")
		return nil
	}
	return BuildForProfile(ProfileSpec{
		Provider:  cfg.EmbeddingProvider,
		APIKey:    apiKey,
		BaseURL:   cfg.EmbeddingBaseURL,
		Model:     cfg.EmbeddingModel,
		Dim:       cfg.EmbeddingDim,
		BatchSize: cfg.EmbeddingBatchSize,
	})
}
