package domain

import (
	"context"
	"time"
)

// Role is the access role of a user within their company.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// Valid reports whether r is a role the system recognises. Anything else —
// including the empty string — is rejected at the edge rather than defaulted,
// because the only safe default here is the one that grants nothing.
func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleMember
}

// User is a dashboard account, always scoped to exactly one company.
type User struct {
	ID           string    `json:"id"`
	CompanyID    string    `json:"company_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`

	// ActivatedAt is nil for a user created by an invite that nobody has
	// accepted yet. DeactivatedAt is non-nil once an admin removes them. Both
	// are checked on every login and every refresh.
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
}

// Active reports whether the account may hold a session.
func (u *User) Active() bool {
	return u.ActivatedAt != nil && u.DeactivatedAt == nil
}

// Pending reports whether the account exists only as an unaccepted invite.
func (u *User) Pending() bool {
	return u.ActivatedAt == nil && u.DeactivatedAt == nil
}

// UserRepository is the persistence contract for users.
type UserRepository interface {
	// Create inserts an already-active user. Signup is its only caller; the
	// invite path uses CreatePending, so forgetting to stamp a timestamp
	// cannot silently produce a user who logs in without accepting.
	Create(ctx context.Context, u *User) error
	CreatePending(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	ListByCompany(ctx context.Context, companyID string) ([]*User, error)

	// Activate sets the password and stamps activated_at in one statement,
	// only for a row that is still pending. It reports ErrNotFound if the user
	// is already active, which is what keeps an invite single-use when two
	// accepts race.
	Activate(ctx context.Context, id, passwordHash string, at time.Time) error
	UpdateRole(ctx context.Context, companyID, id string, role Role) error
	Deactivate(ctx context.Context, companyID, id string, at time.Time) error
	// Delete removes a user outright. Only ever called for a pending user, to
	// free the globally-unique email when an invite is revoked.
	Delete(ctx context.Context, companyID, id string) error
	CountActiveAdmins(ctx context.Context, companyID string) (int, error)
}
