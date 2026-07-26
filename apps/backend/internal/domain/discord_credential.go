package domain

import (
	"context"
	"time"
)

// CompanyDiscordCredential holds one tenant's Discord bot configuration.
// BotTokenEncrypted is AES-256-GCM via crypto.DSNCipher (same key as DSNs).
// PublicKey is the Ed25519 hex string from the Discord developer portal and
// is used only by the interactions HTTP webhook to verify request signatures.
type CompanyDiscordCredential struct {
	CompanyID         string
	ApplicationID     string
	PublicKey         string
	BotTokenEncrypted []byte
	GuildID           string
	Enabled           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CompanyDiscordCredentialRepository persists per-tenant Discord creds.
type CompanyDiscordCredentialRepository interface {
	Get(ctx context.Context, companyID string) (*CompanyDiscordCredential, error)
	GetByApplicationID(ctx context.Context, applicationID string) (*CompanyDiscordCredential, error)
	Upsert(ctx context.Context, c *CompanyDiscordCredential) error
	Delete(ctx context.Context, companyID string) error
	// ListEnabled returns every enabled row so cmd/discord can open one
	// gateway session per tenant on boot.
	ListEnabled(ctx context.Context) ([]*CompanyDiscordCredential, error)
}
