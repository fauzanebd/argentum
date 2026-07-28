package domain

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// Scope is one capability an API key may carry. The vocabulary is closed and
// owned here: a key stores strings, but nothing outside this list can ever be
// written to one, so a typo in a create request is a 400 rather than a scope
// that silently grants nothing until someone debugs it.
//
// Deny by default. A key with no scopes reaches no route, and every scoped
// route names the scope it needs — there is no "any authenticated key" tier.
type Scope string

const (
	// ScopeReadMetrics — the metric registry (T-06/T-07).
	ScopeReadMetrics Scope = "read:metrics"
	// ScopeReadThreads — conversation threads and their messages.
	ScopeReadThreads Scope = "read:threads"
	// ScopeWriteChat — start a turn. The only scope that spends money on its
	// own, which is why it is a write even though the caller is asking a
	// question.
	ScopeWriteChat Scope = "write:chat"
	// ScopeReadUsage — token and credit reporting.
	ScopeReadUsage Scope = "read:usage"
	// ScopeReadAudit — the agent action log (T-05).
	ScopeReadAudit Scope = "read:audit"
	// ScopeWriteActions — execute an action (T-10/T-12).
	ScopeWriteActions Scope = "write:actions"
)

// AllScopes is the vocabulary, in the order the dashboard offers it: reads
// first, writes last, so the two capabilities that cost money are not the
// first two checkboxes under the cursor.
//
// T-A1 adds `write:reports` and `read:documents` when the routes that need
// them exist. A scope with no route behind it is a checkbox that promises
// something, so they are not pre-declared here.
var AllScopes = []Scope{
	ScopeReadMetrics,
	ScopeReadThreads,
	ScopeReadUsage,
	ScopeReadAudit,
	ScopeWriteChat,
	ScopeWriteActions,
}

// Valid reports whether s is a scope this system issues.
func (s Scope) Valid() bool { return slices.Contains(AllScopes, s) }

// NormalizeScopes validates, de-duplicates and orders a requested scope set.
// Ordering is by the AllScopes vocabulary rather than alphabetically so the
// dashboard renders a key's scopes in the same order it offered them.
//
// An unknown scope is rejected rather than dropped: silently minting a key
// with fewer capabilities than the caller asked for produces a 403 later, in a
// different process, with nothing to point at.
func NormalizeScopes(raw []string) ([]Scope, error) {
	want := make(map[Scope]bool, len(raw))
	for _, r := range raw {
		s := Scope(r)
		if !s.Valid() {
			return nil, fmt.Errorf("%w: %q is not a scope", ErrInvalidInput, r)
		}
		want[s] = true
	}
	out := make([]Scope, 0, len(want))
	for _, s := range AllScopes {
		if want[s] {
			out = append(out, s)
		}
	}
	return out, nil
}

// APIKey is a company-scoped machine credential. It authenticates a script,
// not a person: there is no role on it, only scopes, because "admin" is a
// statement about a human's authority over a company and an integration has
// no authority beyond the capabilities it was minted with.
//
// The secret half never appears on this struct. KeyPrefix is the public half
// and the lookup key; KeyHash is what the secret is checked against and is
// json:"-" so that no handler can leak it by returning the record.
type APIKey struct {
	ID        string  `json:"id"`
	CompanyID string  `json:"company_id"`
	Name      string  `json:"name"`
	KeyPrefix string  `json:"key_prefix"`
	KeyHash   string  `json:"-"`
	Scopes    []Scope `json:"scopes"`
	CreatedBy string  `json:"created_by,omitempty"`
	// LastUsedAt is written at most once a minute per key. It answers "is this
	// key still in use?" before a revoke, which is the only question it needs
	// to be accurate enough for.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	// Status is derived, not stored — see Status. It is on the struct so the
	// dashboard does not re-implement the same three-way comparison against
	// two nullable timestamps.
	Status string `json:"status"`
}

// Key status values. A revoked key that has also expired reads as revoked:
// somebody made a decision about it, and that is the more useful fact.
const (
	APIKeyActive  = "active"
	APIKeyRevoked = "revoked"
	APIKeyExpired = "expired"
)

// Usable reports whether the key may authenticate a request at time now.
func (k *APIKey) Usable(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

// StatusAt derives the display status at time now.
func (k *APIKey) StatusAt(now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return APIKeyRevoked
	case k.ExpiresAt != nil && !now.Before(*k.ExpiresAt):
		return APIKeyExpired
	default:
		return APIKeyActive
	}
}

// HasScope reports whether the key carries s.
func (k *APIKey) HasScope(s Scope) bool { return slices.Contains(k.Scopes, s) }

// SortedScopeStrings renders the scopes for storage. Sorted so two keys minted
// with the same capabilities store byte-identical arrays, which makes a
// database read of the table diffable by eye.
func (k *APIKey) SortedScopeStrings() []string {
	out := make([]string, 0, len(k.Scopes))
	for _, s := range k.Scopes {
		out = append(out, string(s))
	}
	slices.Sort(out)
	return out
}

// APIKeyRepository is the persistence contract for machine credentials.
//
// There is no Update: a key's capabilities are fixed at creation. Editing the
// scopes of a credential that is already deployed in someone else's CI config
// changes what that config can do without anyone touching it — the safe
// operation is to mint a new key and revoke the old one, and leaving the
// unsafe one out of the interface is how that stays true.
type APIKeyRepository interface {
	Create(ctx context.Context, k *APIKey) error
	// GetByPrefix returns the key whether or not it is revoked or expired.
	// The caller decides, which is what lets authentication distinguish "no
	// such key" from "this key is revoked" in its logs while still answering
	// the caller with the same 401.
	GetByPrefix(ctx context.Context, prefix string) (*APIKey, error)
	ListByCompany(ctx context.Context, companyID string) ([]*APIKey, error)
	// Revoke is company-scoped so an id from another tenant is a not-found
	// rather than a cross-tenant revocation.
	Revoke(ctx context.Context, companyID, id string, at time.Time) error
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}
