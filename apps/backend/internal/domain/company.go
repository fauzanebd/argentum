package domain

import (
	"context"
	"time"
)

// PIIRedactionMode is a company's policy for the output redaction rules
// (T-07b). The values match guardrails.PIIMode, which is where they are acted
// on; this is the storage and validation half, so the domain does not have to
// import the policy engine to describe a column.
type PIIRedactionMode string

const (
	// PIIRedactionStrict redacts every kind of personal data the rules find.
	PIIRedactionStrict PIIRedactionMode = "strict"
	// PIIRedactionContactOK lets emails and phone numbers through, so a tenant
	// can get the customer contact list they asked for. Identity documents and
	// card numbers are still redacted.
	PIIRedactionContactOK PIIRedactionMode = "contact_ok"
	// PIIRedactionOff runs no redaction rule at all.
	PIIRedactionOff PIIRedactionMode = "off"
)

// PIIRedactionModes lists the settable values, in the order a settings form
// should offer them: most protective first.
func PIIRedactionModes() []PIIRedactionMode {
	return []PIIRedactionMode{PIIRedactionStrict, PIIRedactionContactOK, PIIRedactionOff}
}

// Valid reports whether the mode is one this product knows.
func (m PIIRedactionMode) Valid() bool {
	switch m {
	case PIIRedactionStrict, PIIRedactionContactOK, PIIRedactionOff:
		return true
	default:
		return false
	}
}

// Company is a tenant of Argentum. Every user, phone number, DB connection,
// thread, and usage event is owned by exactly one company.
type Company struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	DefaultCurrency string    `json:"default_currency"` // ISO 4217, e.g. "IDR", "USD"
	CreatedAt       time.Time `json:"created_at"`
	// PIIRedactionMode governs the output redaction rules for this tenant's
	// turns. Empty on a row written before migration 045 and read as strict,
	// which is the column's default.
	PIIRedactionMode PIIRedactionMode `json:"pii_redaction_mode"`
}

// CompanyRepository is the persistence contract for companies.
type CompanyRepository interface {
	Create(ctx context.Context, c *Company) error
	GetByID(ctx context.Context, id string) (*Company, error)
	GetBySlug(ctx context.Context, slug string) (*Company, error)
	Update(ctx context.Context, c *Company) error
}
