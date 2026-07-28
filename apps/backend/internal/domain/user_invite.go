package domain

import (
	"context"
	"time"
)

// UserInvite is a single-use grant that lets one email address activate a
// pending user in one company. The plaintext token is shown to the inviting
// admin exactly once and never stored: TokenHash is what persists.
type UserInvite struct {
	ID         string     `json:"id"`
	CompanyID  string     `json:"company_id"`
	Email      string     `json:"email"`
	Role       Role       `json:"role"`
	TokenHash  string     `json:"-"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	InvitedBy  string     `json:"invited_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Pending reports whether the invite can still be accepted at time now.
func (i *UserInvite) Pending(now time.Time) bool {
	return i.AcceptedAt == nil && now.Before(i.ExpiresAt)
}

// UserInviteRepository is the persistence contract for invites.
type UserInviteRepository interface {
	Create(ctx context.Context, inv *UserInvite) error
	// GetByTokenHash returns the invite regardless of whether it is expired or
	// already accepted; the caller decides, so it can tell an invitee which of
	// the two happened instead of a flat "invalid token".
	GetByTokenHash(ctx context.Context, tokenHash string) (*UserInvite, error)
	ListOpenByCompany(ctx context.Context, companyID string) ([]*UserInvite, error)
	MarkAccepted(ctx context.Context, id string, at time.Time) error
	// DeleteOpenFor removes any unaccepted invite for an address in a company.
	// Used both by re-invite and by revoke.
	DeleteOpenFor(ctx context.Context, companyID, email string) error
}
