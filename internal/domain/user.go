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

// User is a dashboard account, always scoped to exactly one company.
type User struct {
	ID           string    `json:"id"`
	CompanyID    string    `json:"company_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// UserRepository is the persistence contract for users.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	ListByCompany(ctx context.Context, companyID string) ([]*User, error)
}
