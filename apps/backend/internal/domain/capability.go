package domain

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Capability is a named power a person may be granted (T-Z1): something they
// may *do*, with no object attached. Speaking a question is a capability; which
// agent they may speak it to is not — that has an object, and is a resource
// grant (T-Z2). Folding the two into one table would make the resource id
// nullable and every query ambiguous about what a NULL means
// (docs/plan/12-access-grants-roadmap.md §1a).
//
// It is Scope's shape for a human rather than a machine, and deliberately not
// the same type. A key outlives the person who minted it, so a key that
// borrowed its minter's capabilities would widen whenever theirs did (roadmap
// 12, decision 9).
//
// The vocabulary is closed. A policy entry naming a capability that does not
// exist is a compile error, and a grant request naming one is a 400 rather than
// a row that grants nothing until somebody debugs why.
type Capability string

const (
	// CapabilityVoice — speak a question and hear the answer (roadmap 11, Track
	// C, which depends on this ticket rather than building a table of its own).
	CapabilityVoice Capability = "voice"
	// CapabilityApproveActions — decide an action the agent proposed.
	CapabilityApproveActions Capability = "approve_actions"
	// CapabilityExportData — take the company's data out of the product.
	CapabilityExportData Capability = "export_data"
)

// AllCapabilities is the vocabulary, in the order the dashboard offers it.
//
// All three exist before any route asks for one, for the reason AllScopes gives
// about write:reports: a grant made today reaches nothing until a route
// requires it, and a vocabulary that grew only with its routes would make an
// admin wait for a deploy before they could prepare the people who should have
// it. Which routes ask for none of them yet — and why that is deliberate for
// the two that name things the product already does — is written down beside
// capabilityPolicy in cmd/api/policy.go.
var AllCapabilities = []Capability{
	CapabilityVoice,
	CapabilityApproveActions,
	CapabilityExportData,
}

// Valid reports whether c is a capability this system grants.
func (c Capability) Valid() bool { return slices.Contains(AllCapabilities, c) }

// ParseCapability is the normaliser. It trims and lower-cases what arrived — a
// URL segment, typed by a person or built by a client — and refuses anything
// outside the vocabulary. Refused rather than dropped, for NormalizeScopes'
// reason: a grant that silently stored nothing produces a 403 later, on a
// different page, with nothing to point at.
func ParseCapability(raw string) (Capability, error) {
	c := Capability(strings.ToLower(strings.TrimSpace(raw)))
	if !c.Valid() {
		return "", fmt.Errorf("%w: %q is not a capability", ErrInvalidInput, raw)
	}
	return c, nil
}

// CapabilityGrant is one held capability, and who decided it.
type CapabilityGrant struct {
	UserID     string     `json:"user_id"`
	Capability Capability `json:"capability"`
	// GrantedBy is empty once the admin who granted it has been deleted: the
	// grant survives them, and only the attribution does not.
	GrantedBy string    `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at"`
}

// CapabilityRepository is the persistence contract for capability grants.
//
// Every method is company-scoped, and every method answers ErrNotFound when
// userID is not a user of companyID. That is what makes a cross-tenant id a 404
// everywhere: never a grant written against another company's person, and
// never an empty list that reads as "holds nothing" for somebody who is not
// here at all.
type CapabilityRepository interface {
	ListForUser(ctx context.Context, companyID, userID string) ([]CapabilityGrant, error)
	// Grant is idempotent. Granting what is already held succeeds and keeps
	// the original row, so granted_by goes on naming who decided first.
	Grant(ctx context.Context, companyID, userID string, c Capability, grantedBy string) error
	// Revoke is idempotent too: revoking what is not held succeeds. Revoking is
	// deleting the row, and there is no deny rule to write instead (roadmap
	// 12, decision 2).
	Revoke(ctx context.Context, companyID, userID string, c Capability) error
}
