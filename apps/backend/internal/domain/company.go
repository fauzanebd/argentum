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
	// MessageRetentionDays is how long this tenant's conversation transcripts
	// are kept (T-H6). Zero means forever, which is what every row did before
	// migration 067 and therefore what an unset row must keep doing — see
	// [RetentionForever].
	MessageRetentionDays int `json:"message_retention_days"`
}

// RetentionForever is the MessageRetentionDays value that disables the purge.
//
// Named rather than written as a bare 0 at each of its four uses, because the
// difference between "keep forever" and "keep for no time at all" is one
// character and the second one deletes a tenant's history.
const RetentionForever = 0

// MaxMessageRetentionDays bounds what an admin may set. Ten years is longer
// than any retention policy this product will be asked for and short enough
// that a fat-fingered 36500000 is refused rather than stored.
const MaxMessageRetentionDays = 3650

// ValidRetentionDays reports whether days is a settable retention.
func ValidRetentionDays(days int) bool {
	return days >= RetentionForever && days <= MaxMessageRetentionDays
}

// CompanyRepository is the persistence contract for companies.
type CompanyRepository interface {
	Create(ctx context.Context, c *Company) error
	GetByID(ctx context.Context, id string) (*Company, error)
	GetBySlug(ctx context.Context, slug string) (*Company, error)
	Update(ctx context.Context, c *Company) error
}
