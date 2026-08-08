package domain

import (
	"context"
	"time"
)

// CompanySlackCredential holds one tenant's Slack app configuration.
// BotTokenEncrypted is AES-256-GCM via crypto.DSNCipher (same key as DSNs)
// and holds the xoxb- bot user token used for chat.postMessage.
// SigningSecret verifies the v0 request signature on every inbound event.
// BotUserID is the bot's own user id, used to strip the @mention from the
// text and to ignore the bot's own messages; it is optional because the
// webhook can fall back to the `authorizations` array Slack sends.
type CompanySlackCredential struct {
	CompanyID         string
	AppID             string
	TeamID            string
	BotTokenEncrypted []byte
	SigningSecret     string
	BotUserID         string
	Enabled           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CompanySlackCredentialRepository persists per-tenant Slack creds.
type CompanySlackCredentialRepository interface {
	Get(ctx context.Context, companyID string) (*CompanySlackCredential, error)
	GetByAppID(ctx context.Context, appID string) (*CompanySlackCredential, error)
	Upsert(ctx context.Context, c *CompanySlackCredential) error
	Delete(ctx context.Context, companyID string) error
	ListEnabled(ctx context.Context) ([]*CompanySlackCredential, error)
}
