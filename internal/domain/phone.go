package domain

import (
	"context"
	"time"
)

// AllowedPhoneNumber records which phone numbers are authorized to chat with
// Argentum on behalf of a company. Phone numbers are globally unique across
// all companies so inbound webhook traffic can be routed by `From` alone.
type AllowedPhoneNumber struct {
	CompanyID   string    `json:"company_id"`
	PhoneNumber string    `json:"phone_number"`
	Label       string    `json:"label,omitempty"`
	AddedAt     time.Time `json:"added_at"`
}

// PhoneRepository is the persistence contract for the allowlist.
type PhoneRepository interface {
	Add(ctx context.Context, p *AllowedPhoneNumber) error
	Remove(ctx context.Context, companyID, phoneNumber string) error
	ListByCompany(ctx context.Context, companyID string) ([]*AllowedPhoneNumber, error)
	// FindCompanyByPhone resolves the owning company for an inbound phone
	// number. Returns ErrNotFound if the number is not on any company's
	// allowlist.
	FindCompanyByPhone(ctx context.Context, phoneNumber string) (*AllowedPhoneNumber, error)
}
