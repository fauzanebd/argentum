package domain

import (
	"context"
	"time"
)

// LLMTier enumerates per-company LLM profile slots. Missing row for a tier
// means the resolver falls back to env-based defaults.
type LLMTier string

const (
	LLMTierPrimary   LLMTier = "primary"
	LLMTierLight     LLMTier = "light"
	LLMTierEmbedding LLMTier = "embedding"
)

// CompanyLLMCredential is one tier of a tenant LLM override. Any of
// Interface, Model, BaseURL, APIKeyEncrypted may be empty/nil; the resolver
// merges per-field with env defaults.
type CompanyLLMCredential struct {
	ID              string
	CompanyID       string
	Tier            LLMTier
	Interface       string
	Model           string
	BaseURL         string
	APIKeyEncrypted []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CompanyLLMCredentialRepository persists per-tenant LLM overrides.
type CompanyLLMCredentialRepository interface {
	// GetByCompany returns at most 3 rows (one per tier). Empty slice when
	// the tenant has no overrides.
	GetByCompany(ctx context.Context, companyID string) ([]*CompanyLLMCredential, error)
	// Upsert writes (or replaces) the row for (company_id, tier). Empty
	// string fields are stored as NULL.
	Upsert(ctx context.Context, c *CompanyLLMCredential) error
	// Delete removes a tier override; resolver then falls back to env.
	Delete(ctx context.Context, companyID string, tier LLMTier) error
}
