// Package authz answers one question for every request that has a person
// behind it: may this user reach this object (T-Z2). It is the single decision
// function roadmap 12's decision 1 asks for — every seam that selects an agent,
// every route that serves a dashboard, every tool that quotes a document asks
// here, and none of them composes the answer itself.
//
// The rule is three lines (Evaluate) and deliberately knows nothing else. It
// does not know who created the object, because creating something is
// provenance rather than ownership (`056_dashboards.up.sql:50`). It does not know
// the caller's role, because an admin manages grants without holding them
// (decision 4). And it does not know which door the request came through: a
// channel, a key or a widget session has no user to hand it, and what each of
// those doors does instead is T-Z8's decision, not a default this package
// supplies.
package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Loader is the one read a decision needs. Declared here, where it is consumed;
// postgres.ResourceGrantRepo is the production one.
type Loader interface {
	LoadAccess(ctx context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error)
}

// Subject is who is asking.
type Subject struct {
	CompanyID string
	UserID    string
	// Role is carried and never consulted by a decision. It is here so the
	// decision table can be run for an admin and for a member and prove the
	// answers are identical — decision 4 as an assertion rather than as an
	// absence nobody checks — and so a caller that logs a refusal can say whose
	// it was.
	Role domain.Role
}

// Reason says why a decision came out the way it did, as a value, so a refusal
// can explain itself and a counter can be keyed by it (T-Z9) without anybody
// parsing a sentence.
type Reason string

const (
	// ReasonOpen — allowed: the resource is open to every member.
	ReasonOpen Reason = "open"
	// ReasonGranted — allowed: restricted, and this person holds a grant.
	ReasonGranted Reason = "granted"
	// ReasonNotGranted — refused: restricted, and this person does not.
	ReasonNotGranted Reason = "not_granted"
	// ReasonNotFound — refused: no such resource of this kind in this company.
	// A caller should answer it exactly as it answers a missing object, so an
	// id from another company reveals nothing.
	ReasonNotFound Reason = "not_found"
)

// Decision is an answer and its reason.
type Decision struct {
	Allowed bool
	Reason  Reason
}

var errUnconfigured = errors.New("resource authorisation is not configured")

// Evaluate is the rule and nothing else. Everything in this package that decides
// comes through it, which is what makes one table test sufficient.
func Evaluate(access domain.ResourceAccess, found bool) Decision {
	switch {
	case !found:
		return Decision{Reason: ReasonNotFound}
	case access.Mode == domain.AccessModeOpen:
		return Decision{Allowed: true, Reason: ReasonOpen}
	case access.Mode == domain.AccessModeRestricted && access.Granted:
		return Decision{Allowed: true, Reason: ReasonGranted}
	default:
		// Restricted without a grant — and also any mode this code does not
		// recognise. A value the migration's CHECK should have refused is a
		// value nobody decided, and it is read as closed rather than as open.
		return Decision{Reason: ReasonNotGranted}
	}
}

// Authorizer decides against a Loader, and records the refusals its callers act
// on (refusal.go).
type Authorizer struct {
	loader  Loader
	counter RefusalCounter
	audit   AuditWriter
}

// New builds an Authorizer that counts refusals on the process's collector and
// writes none down until WithAudit.
func New(loader Loader) *Authorizer { return &Authorizer{loader: loader, counter: processCounter()} }

// Decide answers for one resource. An error means the answer could not be read,
// and every caller must treat it as a refusal.
func (a *Authorizer) Decide(ctx context.Context, s Subject, kind domain.ResourceKind, id string) (Decision, error) {
	decisions, err := a.decideAll(ctx, s, kind, []string{id})
	if err != nil {
		return Decision{}, err
	}
	return decisions[0], nil
}

// Visible returns the ids the subject may reach, in the order given.
//
// It exists because the agent picker and the dashboard list each ask about N
// objects on every render, and N round trips per page is how an access check
// becomes the reason a page is slow. However many ids arrive, the loader is
// asked once.
func (a *Authorizer) Visible(ctx context.Context, s Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	decisions, err := a.decideAll(ctx, s, kind, ids)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for i, id := range ids {
		if decisions[i].Allowed {
			out = append(out, id)
		}
	}
	return out, nil
}

func (a *Authorizer) decideAll(ctx context.Context, s Subject, kind domain.ResourceKind, ids []string) ([]Decision, error) {
	if a == nil || a.loader == nil {
		return nil, errUnconfigured
	}
	if !kind.Valid() {
		return nil, fmt.Errorf("%w: %q is not a resource kind", domain.ErrInvalidInput, kind)
	}

	// Ids are matched case-insensitively and de-duplicated before the load.
	// Postgres renders a uuid in lower case, and an id typed into a URL in upper
	// case is the same object — reading it as not-found would refuse somebody
	// for their capitalisation.
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		key := idKey(id)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, key)
	}

	var access map[string]domain.ResourceAccess
	if s.CompanyID != "" && len(unique) > 0 {
		loaded, err := a.loader.LoadAccess(ctx, s.CompanyID, s.UserID, kind, unique)
		if err != nil {
			return nil, fmt.Errorf("load %s access: %w", kind, err)
		}
		access = loaded
	}

	decisions := make([]Decision, len(ids))
	for i, id := range ids {
		acc, found := access[idKey(id)]
		decisions[i] = Evaluate(acc, found)
	}
	return decisions, nil
}

func idKey(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
