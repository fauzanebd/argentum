package domain

import (
	"context"
	"time"
)

// AllowedLarkUser records which Lark open_ids are authorized to chat with
// the agent on behalf of a company. A user may be allowed by more than one
// company (through different Lark apps), so the primary key is composite.
type AllowedLarkUser struct {
	CompanyID  string    `json:"company_id"`
	LarkOpenID string    `json:"lark_open_id"`
	Label      string    `json:"label,omitempty"`
	AddedAt    time.Time `json:"added_at"`
}

// AllowedLarkUserRepository is the persistence contract for the Lark
// allowlist.
type AllowedLarkUserRepository interface {
	Add(ctx context.Context, u *AllowedLarkUser) error
	Remove(ctx context.Context, companyID, larkOpenID string) error
	ListByCompany(ctx context.Context, companyID string) ([]*AllowedLarkUser, error)
	IsAllowed(ctx context.Context, companyID, larkOpenID string) (bool, error)
}
