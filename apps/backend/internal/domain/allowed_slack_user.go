package domain

import (
	"context"
	"time"
)

// AllowedSlackUser records which Slack user ids are authorized to chat with
// the agent on behalf of a company. A user may be allowed by more than one
// company (through different Slack apps), so the primary key is composite.
type AllowedSlackUser struct {
	CompanyID   string    `json:"company_id"`
	SlackUserID string    `json:"slack_user_id"`
	Label       string    `json:"label,omitempty"`
	AddedAt     time.Time `json:"added_at"`
}

// AllowedSlackUserRepository is the persistence contract for the Slack
// allowlist.
type AllowedSlackUserRepository interface {
	Add(ctx context.Context, u *AllowedSlackUser) error
	Remove(ctx context.Context, companyID, slackUserID string) error
	ListByCompany(ctx context.Context, companyID string) ([]*AllowedSlackUser, error)
	IsAllowed(ctx context.Context, companyID, slackUserID string) (bool, error)
}
