package domain

import (
	"context"
	"encoding/json"
	"time"
)

// InvocationStatus is where an action proposal sits in its lifecycle (T-10).
//
//	proposed ─approve─▶ approved ─execute─▶ executed
//	   │                                └──▶ failed
//	   ├─reject──▶ rejected
//	   └─(24h)───▶ expired
//
// Named InvocationStatus, not ActionStatus: ActionStatus already exists on the
// audit log (agent_action.go) and means how a *tool call* ended. These are
// different alphabets for different tables, and collapsing them would let a
// value legal in one row leak into the other.
type InvocationStatus string

const (
	InvocationProposed InvocationStatus = "proposed"
	InvocationApproved InvocationStatus = "approved"
	InvocationRejected InvocationStatus = "rejected"
	InvocationExecuted InvocationStatus = "executed"
	InvocationFailed   InvocationStatus = "failed"
	InvocationExpired  InvocationStatus = "expired"
)

// Valid reports whether s is a status this system writes.
func (s InvocationStatus) Valid() bool {
	switch s {
	case InvocationProposed, InvocationApproved, InvocationRejected,
		InvocationExecuted, InvocationFailed, InvocationExpired:
		return true
	}
	return false
}

// CompanyAction is one tenant's configuration of one action kind (T-10): whether
// it is enabled, whether a proposal of it still needs a human decision, the roles
// that may make that decision, and the sealed configuration the action carries.
//
// Nothing can be proposed for a kind that is not enabled here — the agent's
// write-capable surface is exactly what the admin has switched on, and no wider.
type CompanyAction struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Kind      string `json:"action_kind"`
	Enabled   bool   `json:"enabled"`
	// RequiresApproval defaults true. False is an explicit admin opt-in that lets
	// a proposal execute immediately — it never becomes the default a company
	// drifts into, and it does not turn off the audit trail.
	RequiresApproval bool `json:"requires_approval"`
	// ConfigEncrypted is the action's sealed configuration (an http_action's
	// credentials, T-12b), nil for an action that needs none. Decrypted by the
	// service that holds the cipher, never by the repository.
	ConfigEncrypted []byte `json:"-"`
	// AllowedRoles are the roles permitted to approve this kind. Empty means any
	// member. Enforced at the approval endpoint (T-11).
	AllowedRoles []string  `json:"allowed_roles"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ActionInvocation is one proposal moving through the lifecycle above (T-10).
//
// ParamsRedacted holds the parameters with credential-shaped values stripped;
// because an action's real secret lives in CompanyAction.ConfigEncrypted, a
// well-formed proposal's parameters carry nothing redaction removes, and the
// executor runs off this field rather than a second unredacted one that would
// put secrets in the ledger.
type ActionInvocation struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Kind      string `json:"action_kind"`
	// ParamsRedacted is json.RawMessage so the pending-approvals endpoint returns
	// the object itself rather than a base64 blob (see AgentAction.ArgsRedacted).
	ParamsRedacted json.RawMessage  `json:"params_redacted"`
	IdempotencyKey string           `json:"idempotency_key"`
	Status         InvocationStatus `json:"status"`
	ProposedAt     time.Time        `json:"proposed_at"`
	DecidedAt      *time.Time       `json:"decided_at,omitempty"`
	DecidedBy      string           `json:"decided_by,omitempty"`
	ExecutedAt     *time.Time       `json:"executed_at,omitempty"`
	Result         json.RawMessage  `json:"result,omitempty"`
	ErrorText      string           `json:"error_text,omitempty"`
}

// ActionRepository is the persistence contract for the action framework (T-10).
//
// The transitions that decide "exactly once" — Approve above all — run inside a
// SELECT ... FOR UPDATE transaction here rather than in the service, because the
// guarantee is a database one: only the caller that actually moves a row from
// proposed to approved may execute it, and that fact has to survive two requests
// racing. The service decides policy; the repository owns atomicity.
type ActionRepository interface {
	// --- company_actions (the switchboard) ---

	// GetCompanyAction returns one company's configuration for a kind, or
	// ErrNotFound if the kind is not configured (which reads as "not enabled").
	GetCompanyAction(ctx context.Context, companyID, kind string) (*CompanyAction, error)
	// ListCompanyActions returns every kind a company has configured.
	ListCompanyActions(ctx context.Context, companyID string) ([]*CompanyAction, error)
	// UpsertCompanyAction enables or reconfigures a kind for a company, keyed on
	// (company_id, action_kind).
	UpsertCompanyAction(ctx context.Context, a *CompanyAction) error

	// --- action_invocations (the ledger) ---

	// CreateInvocation inserts a proposal, idempotent on (company_id,
	// idempotency_key): a second create with the same key returns the first
	// invocation with created=false rather than a duplicate, so a retried tool
	// call cannot raise two approvable proposals for one intent.
	CreateInvocation(ctx context.Context, inv *ActionInvocation) (stored *ActionInvocation, created bool, err error)
	// GetInvocation returns one invocation, company-scoped.
	GetInvocation(ctx context.Context, companyID, id string) (*ActionInvocation, error)
	// ListInvocations returns a company's invocations newest first.
	ListInvocations(ctx context.Context, companyID string, limit, offset int) ([]*ActionInvocation, error)
	// ListPending returns a company's proposals still awaiting a decision.
	ListPending(ctx context.Context, companyID string) ([]*ActionInvocation, error)

	// Approve atomically moves a proposal to approved, and reports whether this
	// call is the one that did it. transitioned is true only for the caller that
	// moved proposed→approved; every other caller (a concurrent approve, a
	// re-approve of an already-approved or executed row) gets transitioned=false
	// and must not execute. A proposal older than expireBefore is moved to
	// expired instead and returns ErrActionExpired; an already-rejected one
	// returns ErrConflict.
	Approve(ctx context.Context, companyID, id, decidedBy string, now, expireBefore time.Time) (inv *ActionInvocation, transitioned bool, err error)
	// Reject moves a proposal to rejected. Idempotent from rejected; ErrConflict
	// from any decided or executed state, because rejecting something already
	// carried out is not a thing that can be true.
	Reject(ctx context.Context, companyID, id, decidedBy string, now time.Time) (*ActionInvocation, error)

	// MarkExecuted records a successful run, but only for a row still approved —
	// the WHERE status = 'approved' is what keeps a late second executor from
	// overwriting a result.
	MarkExecuted(ctx context.Context, companyID, id string, result json.RawMessage, now time.Time) error
	// MarkFailed records a failed run, likewise only from approved.
	MarkFailed(ctx context.Context, companyID, id, errText string, now time.Time) error
}
