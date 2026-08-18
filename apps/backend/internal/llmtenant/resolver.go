// Package llmtenant resolves per-tenant LLM credentials and caches the
// resulting clients. Each tier (primary / light / embedding) may be
// overridden per company; missing overrides fall back to env defaults.
package llmtenant

import (
	"context"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// EffectiveProfile is the merged (tenant-override + env-default) spec for one
// LLM tier. Used to build the actual client and as the cache version key.
//
// For LLM tiers (primary, light) Interface is "openai" | "anthropic" |
// "gemini". For the embedding tier it stores the provider (currently always
// "openai"). Dim and BatchSize are only populated for embedding profiles —
// they come from env config since tenants don't override them.
type EffectiveProfile struct {
	Interface string
	Model     string
	BaseURL   string
	APIKey    string

	// ZDR mirrors cfg.LLMZDR onto the LLM tiers. It is env-only — a tenant
	// row cannot loosen a privacy guarantee the deployment made — so it never
	// varies between profiles of the same process and is deliberately absent
	// from Version. Unset for the embedding tier: OpenRouter serves no
	// embeddings endpoint, so there is nothing there to route.
	ZDR bool

	Dim       int // embedding only
	BatchSize int // embedding only

	// Version fingerprints the profile. Changes whenever any tenant-settable
	// field above would change, so the client cache busts on rotation.
	Version string
}

// Resolver merges env defaults with a per-tenant row, per tier. Stateless;
// the cache memoizes results.
type Resolver struct {
	repo   domain.CompanyLLMCredentialRepository
	cipher *crypto.DSNCipher
	cfg    *config.Config
}

func NewResolver(repo domain.CompanyLLMCredentialRepository, cipher *crypto.DSNCipher, cfg *config.Config) *Resolver {
	return &Resolver{repo: repo, cipher: cipher, cfg: cfg}
}

// Resolve returns the effective profile for (companyID, tier). When
// companyID is empty, returns the env-default profile. When the repo row's
// company_id disagrees with the requested companyID, returns
// domain.ErrUnauthorized (defense-in-depth against repo bugs).
func (r *Resolver) Resolve(ctx context.Context, companyID string, tier domain.LLMTier) (*EffectiveProfile, error) {
	env := r.envProfile(tier)
	if companyID == "" {
		return env, nil
	}
	rows, err := r.repo.GetByCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("load llm credentials: %w", err)
	}
	var override *domain.CompanyLLMCredential
	for _, row := range rows {
		if row.CompanyID != companyID {
			return nil, domain.ErrUnauthorized
		}
		if row.Tier == tier {
			override = row
			break
		}
	}
	if override == nil {
		return env, nil
	}
	merged, err := r.merge(env, override)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

func (r *Resolver) envProfile(tier domain.LLMTier) *EffectiveProfile {
	switch tier {
	case domain.LLMTierPrimary:
		return &EffectiveProfile{
			Interface: r.cfg.EffectiveLLMInterface(),
			Model:     r.cfg.LLMModel,
			BaseURL:   r.cfg.LLMBaseURL,
			APIKey:    r.cfg.LLMAPIKey,
			ZDR:       r.cfg.LLMZDR,
			Version:   "env:primary",
		}
	case domain.LLMTierLight:
		// Mirror llmclient.BuildLight: when LIGHT_LLM_API_KEY is empty,
		// fall back to the primary LLM's credentials and interface.
		if r.cfg.LightLLMAPIKey == "" {
			p := r.envProfile(domain.LLMTierPrimary)
			p.Version = "env:light->primary"
			return p
		}
		return &EffectiveProfile{
			Interface: r.cfg.EffectiveLightLLMInterface(),
			Model:     r.cfg.LightLLMModel,
			BaseURL:   r.cfg.LightLLMBaseURL,
			APIKey:    r.cfg.LightLLMAPIKey,
			ZDR:       r.cfg.LLMZDR,
			Version:   "env:light",
		}
	case domain.LLMTierEmbedding:
		provider := r.cfg.EmbeddingProvider
		if provider == "" {
			provider = "openai"
		}
		return &EffectiveProfile{
			Interface: strings.ToLower(provider),
			Model:     r.cfg.EmbeddingModel,
			BaseURL:   r.cfg.EmbeddingBaseURL,
			APIKey:    r.cfg.EffectiveEmbeddingAPIKey(),
			Dim:       r.cfg.EmbeddingDim,
			BatchSize: r.cfg.EmbeddingBatchSize,
			Version:   "env:embedding",
		}
	}
	return &EffectiveProfile{}
}

// merge overlays the row's non-empty fields on top of the env profile and
// decrypts the API key if present.
func (r *Resolver) merge(env *EffectiveProfile, row *domain.CompanyLLMCredential) (*EffectiveProfile, error) {
	out := *env
	if s := strings.TrimSpace(strings.ToLower(row.Interface)); s != "" {
		out.Interface = s
	}
	if s := strings.TrimSpace(row.Model); s != "" {
		out.Model = s
	}
	if s := strings.TrimSpace(row.BaseURL); s != "" {
		out.BaseURL = s
	}
	if len(row.APIKeyEncrypted) > 0 {
		plain, err := r.cipher.Decrypt(row.APIKeyEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt llm api key: %w", err)
		}
		out.APIKey = plain
	}
	out.Version = fmt.Sprintf("row:%s:%s", row.Tier, row.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"))
	return &out, nil
}
