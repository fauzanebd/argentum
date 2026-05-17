package domain

import (
	"context"
	"time"
)

// CompanyLarkCredential holds one tenant's Lark (Feishu) app configuration.
// AppSecretEncrypted is AES-256-GCM via crypto.DSNCipher (same key as DSNs).
// VerificationToken authenticates non-encrypted callbacks. EncryptKey (if
// set) decrypts AES-256-CBC encrypted event payloads and is also part of the
// X-Lark-Signature input. BotOpenID is the bot's own open_id, used by the
// inbound dispatcher to detect @mentions of the bot.
type CompanyLarkCredential struct {
	CompanyID          string
	AppID              string
	AppSecretEncrypted []byte
	VerificationToken  string
	EncryptKey         string
	BotOpenID          string
	Enabled            bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CompanyLarkCredentialRepository persists per-tenant Lark creds.
type CompanyLarkCredentialRepository interface {
	Get(ctx context.Context, companyID string) (*CompanyLarkCredential, error)
	GetByAppID(ctx context.Context, appID string) (*CompanyLarkCredential, error)
	Upsert(ctx context.Context, c *CompanyLarkCredential) error
	Delete(ctx context.Context, companyID string) error
	ListEnabled(ctx context.Context) ([]*CompanyLarkCredential, error)
}
