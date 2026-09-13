package domain

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ResourceKind names a kind of object an admin can put behind a grant (T-Z2).
// A capability has no object; a resource grant always names one, and the two are
// separate mechanisms so a resource id is never nullable (roadmap 12 §1a).
//
// The vocabulary is closed, and a new kind is a migration as well as a constant:
// each kind's id lives in its own foreign-key column on resource_grants, which is
// what lets deleting the object delete its grants.
type ResourceKind string

const (
	// ResourceKindAgent — who may talk to which agent (T-Z4).
	ResourceKindAgent ResourceKind = "agent"
	// ResourceKindDashboard — who may open which native dashboard (T-Z5).
	ResourceKindDashboard ResourceKind = "dashboard"
	// ResourceKindConnection — a data source, `db_connections`. A grant on one
	// gates the surfaces a person reaches it through, **not** what an agent may
	// query: that is agent_sources and T-H12's allowlist, and treating a user
	// grant as a data boundary would make it look like one it is not (T-Z6).
	ResourceKindConnection ResourceKind = "connection"
	// ResourceKindDocument — an uploaded source document, `source_documents`
	// (T-P1): the kind search_documents quotes from. Not a generated report in
	// `documents`, which is output somebody asked for rather than a thing read.
	ResourceKindDocument ResourceKind = "document"
)

// AllResourceKinds is the vocabulary, in the order the dashboard groups a
// person's grants.
var AllResourceKinds = []ResourceKind{
	ResourceKindAgent,
	ResourceKindDashboard,
	ResourceKindConnection,
	ResourceKindDocument,
}

// Valid reports whether k is a kind this system can restrict.
func (k ResourceKind) Valid() bool { return slices.Contains(AllResourceKinds, k) }

// ParseResourceKind normalises a kind that arrived in a URL and refuses anything
// outside the vocabulary, for ParseCapability's reason.
func ParseResourceKind(raw string) (ResourceKind, error) {
	k := ResourceKind(strings.ToLower(strings.TrimSpace(raw)))
	if !k.Valid() {
		return "", fmt.Errorf("%w: %q is not a resource kind", ErrInvalidInput, raw)
	}
	return k, nil
}

// AccessMode is whether reaching a resource needs a grant at all.
type AccessMode string

const (
	// AccessModeOpen — every member of the company may reach it, with no grant
	// row. The default for every resource that exists and every one created,
	// because it is what the product did before grants existed (decision 3).
	AccessModeOpen AccessMode = "open"
	// AccessModeRestricted — only people holding a grant may reach it. An admin
	// is not people-holding-a-grant by rank (decision 4), so restricting a
	// resource nobody has been granted makes it reachable by nobody, including
	// whoever created it.
	AccessModeRestricted AccessMode = "restricted"
)

// Valid reports whether m is a mode this system stores.
func (m AccessMode) Valid() bool { return m == AccessModeOpen || m == AccessModeRestricted }

// ParseAccessMode normalises and validates a mode from a request body.
func ParseAccessMode(raw string) (AccessMode, error) {
	m := AccessMode(strings.ToLower(strings.TrimSpace(raw)))
	if !m.Valid() {
		return "", fmt.Errorf("%w: %q is not an access mode; use open or restricted", ErrInvalidInput, raw)
	}
	return m, nil
}

// ResourceGrant is one person's grant on one resource.
type ResourceGrant struct {
	UserID     string       `json:"user_id"`
	Kind       ResourceKind `json:"resource_kind"`
	ResourceID string       `json:"resource_id"`
	// GrantedBy is empty once the admin who granted it has been deleted.
	GrantedBy string    `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at"`
}

// ResourceAccessView is one resource's access as an admin reads it: its mode and
// everyone granted. The mode travels with the grants so a page cannot show a
// list of grants beside a mode read at a different moment.
type ResourceAccessView struct {
	Kind       ResourceKind `json:"resource_kind"`
	ResourceID string       `json:"resource_id"`
	// Name is what the resource is called — an agent's name, a dashboard's
	// title, a source's label, a document's filename — and nothing else from
	// inside it (T-Z5). It is here because the admin managing a restricted
	// dashboard may be refused it: the dashboard list is narrowed by grant for
	// admins too, so without a name on this read the dashboard they locked
	// themselves out of would be an id on Settings → Team with no way to tell
	// which one to open again.
	Name       string          `json:"name"`
	AccessMode AccessMode      `json:"access_mode"`
	Grants     []ResourceGrant `json:"grants"`
}

// AccessModeChange is what flipping a resource's mode did, beyond the flip.
//
// RevokedShares is the dashboard's share links the flip took back (T-Z5). A
// link is a door with no person behind it — a grant cannot follow it to
// whoever holds the URL — so restricting a dashboard revokes every live one in
// the same transaction, and the count is how the admin who pressed it learns
// that it did. Zero for every other kind, and for re-opening: opening a
// dashboard brings no revoked link back.
type AccessModeChange struct {
	AccessMode    AccessMode `json:"access_mode"`
	RevokedShares int        `json:"revoked_shares"`
}

// ResourceAccess is what a decision needs to know about one resource for one
// person, and nothing more: its mode, and whether that person holds a grant.
type ResourceAccess struct {
	Mode    AccessMode `json:"access_mode"`
	Granted bool       `json:"granted"`
}

// ResourceGrantRepository is the persistence contract for resource access.
//
// Every method is company-scoped. A resource or user id that belongs to another
// company is absent from LoadAccess's result and ErrNotFound everywhere else —
// never a grant written across a tenant boundary, and never a mode flipped on
// another company's dashboard.
type ResourceGrantRepository interface {
	// LoadAccess answers for every id at once, in one statement, and omits ids
	// that are not resources of this kind in this company. An empty userID
	// holds no grants.
	LoadAccess(ctx context.Context, companyID, userID string, kind ResourceKind, ids []string) (map[string]ResourceAccess, error)
	View(ctx context.Context, companyID string, kind ResourceKind, id string) (*ResourceAccessView, error)
	// ListViews is View for every resource of a kind in the company, from one
	// statement, including resources nobody is granted. Settings → Team draws
	// both directions of its matrix from it (T-Z7).
	ListViews(ctx context.Context, companyID string, kind ResourceKind) ([]ResourceAccessView, error)
	ListForUser(ctx context.Context, companyID, userID string) ([]ResourceGrant, error)
	// SetAccessMode touches the mode, and no grant row is written or removed,
	// so restricting and re-opening leaves every grant where it was. Restricting
	// a dashboard also revokes its live share links, in the same transaction as
	// the flip, so no link minted beside it can outlive it (T-Z5).
	SetAccessMode(ctx context.Context, companyID string, kind ResourceKind, id string, mode AccessMode) (AccessModeChange, error)
	// Grant is idempotent, and ErrNotFound unless both the user and the
	// resource belong to companyID.
	Grant(ctx context.Context, companyID, userID string, kind ResourceKind, id, grantedBy string) error
	// Revoke is idempotent, with Grant's not-found rule.
	Revoke(ctx context.Context, companyID, userID string, kind ResourceKind, id string) error
}
