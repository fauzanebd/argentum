// Package actions is the write-capable half of what the agent can do (T-10).
//
// Every other capability the agent has reads: it queries a warehouse, evaluates
// a metric, renders a document. An Action changes something outside Argentum —
// it sends a message, it calls an endpoint — and so it is the one capability the
// agent may *propose* but never *perform*. The proposal, the human decision, and
// the exactly-once execution live in app.ActionService; this package is only the
// contract each concrete action implements and the registry that names them.
//
// No concrete actions ship in T-10. send_message is T-12a and http_action is
// T-12b; until one of them registers, an agent that proposes an action is told
// the kind is not available, which is the correct answer on a deployment that
// has enabled none.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Action is one thing the agent can propose to do (T-10).
//
// The three inspection methods — Kind, Describe, Validate — run at propose time,
// before anything is written and long before anything is executed, so a
// malformed or unexplainable proposal never reaches a human. Execute runs only
// after approval, and only from ActionService, never from the agent's tool.
type Action interface {
	// Kind is the stable identifier stored on company_actions and
	// action_invocations, e.g. "send_message". It must match the action_kind an
	// admin enables.
	Kind() string
	// Describe renders the one human-readable sentence shown on the approval card
	// — "Send 'Q3 is up 12%' to the #finance Lark chat". It is what the approver
	// reads instead of the raw parameters, so it must be faithful to what Execute
	// will do. Returns an error only if the parameters cannot be described, which
	// for a validated proposal should not happen.
	Describe(params json.RawMessage) (string, error)
	// Validate checks the parameters are well-formed and safe to store, without
	// performing the action. It is the gate that keeps an un-runnable proposal out
	// of the ledger.
	Validate(params json.RawMessage) error
	// Execute performs the action. It runs post-approval from ActionService with
	// the tenant already on the context (tenantctx.CompanyID); its return value is
	// stored on the invocation as the result. An error here transitions the
	// invocation to failed.
	Execute(ctx context.Context, params json.RawMessage) (result json.RawMessage, err error)
}

// Registry is the set of action kinds this deployment can run. It is populated at
// boot from whichever concrete actions are wired in — none in T-10 — and read by
// ActionService to resolve a kind before proposing or executing it.
type Registry struct {
	byKind map[string]Action
}

// NewRegistry builds a registry from the given actions. A duplicate Kind is a
// programming error and panics at construction, which happens at boot: two
// actions answering to one kind is a wiring mistake that must stop the process,
// not a runtime branch.
func NewRegistry(as ...Action) *Registry {
	r := &Registry{byKind: make(map[string]Action, len(as))}
	for _, a := range as {
		if a == nil {
			continue
		}
		if _, dup := r.byKind[a.Kind()]; dup {
			panic(fmt.Sprintf("actions: duplicate action kind %q", a.Kind()))
		}
		r.byKind[a.Kind()] = a
	}
	return r
}

// Get returns the action for a kind, or ok=false when the deployment has none.
func (r *Registry) Get(kind string) (Action, bool) {
	if r == nil {
		return nil, false
	}
	a, ok := r.byKind[kind]
	return a, ok
}

// Kinds is every registered kind, sorted, for a "which actions can I propose"
// listing and for error messages that name what is available.
func (r *Registry) Kinds() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.byKind))
	for k := range r.byKind {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
