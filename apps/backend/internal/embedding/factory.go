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

// EnvKeyResolves reports whether this process's environment supplies a usable
// embedding credential. A tenant row can still supply one per company — this is
// the deployment-wide default, which is what a boot log can honestly speak
// about.
func EnvKeyResolves(cfg *config.Config) bool {
	if !cfg.EmbeddingEnabled {
		return false
	}
	if strings.TrimSpace(cfg.EffectiveEmbeddingAPIKey()) == "" {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider))
	return provider == "" || provider == "openai"
}

// LogEnvCoverage says at boot what the embedding credential does and does not
// buy on this deployment. Called once per process, beside the cache it
// describes.
//
// **This is the line that was missing, and its absence is the finding.** The
// warning below used to live in a `Build` function that the per-tenant
// `llmtenant.EmbeddingCache` replaced — and nothing called `Build` afterwards,
// so the one sentence telling an operator their embeddings were off went with
// it. `EmbeddingCache.For` returns `(nil, nil)` for a company with no key: no
// error, no log, three features silently inert, and `EMBEDDING_ENABLED=true`
// still printing "table-picker embeddings enabled". T-P8's live gate,
// 2026-08-19, spent a sitting establishing by hand what this line says.
func LogEnvCoverage(cfg *config.Config) {
	if !cfg.EmbeddingEnabled {
		logrus.Info("embedding: EMBEDDING_ENABLED=false — the table picker, cookbook retrieval and the dense half of document search are off; document search answers from the lexical index alone")
		return
	}
	if provider := strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider)); provider != "" && provider != "openai" {
		logrus.WithField("provider", cfg.EmbeddingProvider).
			Warn("embedding: only the 'openai' provider is supported — the table picker, cookbook retrieval and the dense half of document search are off for every company without its own credential row")
		return
	}
	if strings.TrimSpace(cfg.EffectiveEmbeddingAPIKey()) == "" {
		logrus.Warn("embedding: EMBEDDING_API_KEY is not set, and the primary LLM key cannot be borrowed — it is either not an OpenAI-interface key or it belongs to a different host than EMBEDDING_BASE_URL. The table picker, cookbook retrieval and the dense half of document search are OFF for every company without its own credential row; set EMBEDDING_API_KEY to enable them")
		return
	}
	logrus.WithFields(logrus.Fields{
		"model": cfg.EmbeddingModel,
		"dim":   cfg.EmbeddingDim,
	}).Info("embedding: an environment credential resolves; the table picker, cookbook retrieval and the dense half of document search are available")
}
