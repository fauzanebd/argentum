package domain

import (
	"context"
	"time"
)

// Company is a tenant of Argentum. Every user, phone number, DB connection,
// thread, and usage event is owned by exactly one company.
type Company struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	DefaultCurrency string    `json:"default_currency"` // ISO 4217, e.g. "IDR", "USD"
	CreatedAt       time.Time `json:"created_at"`
}

// CompanyRepository is the persistence contract for companies.
type CompanyRepository interface {
	Create(ctx context.Context, c *Company) error
	GetByID(ctx context.Context, id string) (*Company, error)
	GetBySlug(ctx context.Context, slug string) (*Company, error)
	Update(ctx context.Context, c *Company) error
}
