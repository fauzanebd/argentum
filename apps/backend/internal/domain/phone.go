package domain

import (
	"context"
	"strings"
	"time"
)

// NormalizePhone strips the `whatsapp:` prefix and surrounding whitespace so a
// number is stored and compared in one canonical E.164 form.
//
// It lives in domain rather than beside the allowlist repository that used to
// own it because T-S4 compares a *second* table's phone column against inbound
// traffic: a `whatsapp:` prefix on one side of that comparison and not the
// other is a binding that silently never matches, and the only way two writers
// stay in one shape is one function.
func NormalizePhone(p string) string {
	return strings.TrimPrefix(strings.TrimSpace(p), "whatsapp:")
}

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
