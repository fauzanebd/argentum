package domain

import (
	"context"
	"time"
)

// AllowedDiscordUser records which Discord user IDs are authorized to chat
// with the agent on behalf of a company. A user may be allowed by more than
// one company, so the primary key is composite.
type AllowedDiscordUser struct {
	CompanyID     string    `json:"company_id"`
	DiscordUserID string    `json:"discord_user_id"`
	Label         string    `json:"label,omitempty"`
	AddedAt       time.Time `json:"added_at"`
}

// AllowedDiscordUserRepository is the persistence contract for the
// Discord allowlist.
type AllowedDiscordUserRepository interface {
	Add(ctx context.Context, u *AllowedDiscordUser) error
	Remove(ctx context.Context, companyID, discordUserID string) error
	ListByCompany(ctx context.Context, companyID string) ([]*AllowedDiscordUser, error)
	// IsAllowed reports whether (companyID, discordUserID) is on the
	// allowlist. ErrNotFound is mapped to (false, nil) by callers if they
	// don't care about the distinction.
	IsAllowed(ctx context.Context, companyID, discordUserID string) (bool, error)
}
