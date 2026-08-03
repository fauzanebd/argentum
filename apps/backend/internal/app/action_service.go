// Package app — the action framework (T-10), Argentum's write-capable agency.
//
// Every other tool the agent has only reads. This is the first surface through
// which it changes something outside the product, so it is the first that cannot
// run on the agent's word alone: the agent *proposes* an action, a human
// *approves* it, and only then — from here, never from the tool — is it carried
// out, exactly once. Tenant SQL stays read-only permanently; no action routes
// through it.
//
// ActionService owns the state machine. The exactly-once guarantee is not this
// file's cleverness — it is domain.ActionRepository.Approve, which moves a
// proposal from proposed to approved under a row lock and tells exactly one
// caller it may execute. This service decides policy (is the kind enabled, does
// it still need approval, who is accountable) and records every proposal and
// decision in the audit log.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools"
)

// actionProposalTTL is how long a proposal stays approvable. Past it, approving
// is refused and the proposal is marked expired — "propose it again" is the safe
// answer to a stale write, and a day is long enough for a human to see and act on
// an approval card without leaving an ancient one live indefinitely.
const actionProposalTTL = 24 * time.Hour

// ActionAudit is the one write the service makes to the audit log: a decision on
// a proposal (T-05). domain.AgentActionRepository satisfies it. Narrow on
// purpose — the service records decisions, it never reads or edits the log.
type ActionAudit interface {
	Create(ctx context.Context, a *domain.AgentAction) error
}

// ActionService is the propose/approve/reject/execute state machine (T-10).
type ActionService struct {
	repo     domain.ActionRepository
	registry *actions.Registry
	audit    ActionAudit

	// now and newKey are injectable so the state machine — expiry above all — is
	// testable without sleeping a day, and so a test can force an idempotency key
	// collision.
	now    func() time.Time
	newKey func() string
}

// NewActionService wires the framework. A nil registry is legal and means the
// deployment has no actions yet (the T-10 state, before T-12a): every proposal is
// then refused with "no such action", which is the correct answer.
func NewActionService(repo domain.ActionRepository, registry *actions.Registry, audit ActionAudit) *ActionService {
	return &ActionService{
		repo:     repo,
		registry: registry,
		audit:    audit,
		now:      time.Now,
		newKey:   uuid.NewString,
	}
}

// --- propose (agent path) ---

// ProposeAction records a proposal for an enabled action kind and returns its id.
// It never executes on the requires_approval path; on the admin opt-out path it
// executes immediately, because that is exactly what the opt-out means. Satisfies
// tools.ActionProposer, so propose_action calls straight through.
func (s *ActionService) ProposeAction(ctx context.Context, in tools.ProposeActionInput) (*tools.ProposeActionResult, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return nil, fmt.Errorf("no tenant in context")
	}

	action, ok := s.registry.Get(in.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: no action named %q is available; enabled kinds are %v",
			domain.ErrInvalidInput, in.Kind, s.registry.Kinds())
	}
	if err := action.Validate(in.Params); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err)
	}

	cfg, err := s.repo.GetCompanyAction(ctx, companyID, in.Kind)
	if errors.Is(err, domain.ErrNotFound) || (cfg != nil && !cfg.Enabled) {
		return nil, fmt.Errorf("%w: the %q action is not enabled for this workspace; an admin can turn it on in Settings",
			domain.ErrInvalidInput, in.Kind)
	}
	if err != nil {
		return nil, err
	}

	description, dErr := action.Describe(in.Params)
	if dErr != nil || description == "" {
		description = in.Kind
	}

	now := s.now()
	inv := &domain.ActionInvocation{
		CompanyID:      companyID,
		ThreadID:       tenantctx.ThreadID(ctx),
		MessageID:      tenantctx.MessageID(ctx),
		Kind:           in.Kind,
		ParamsRedacted: tools.RedactJSON(in.Params),
		IdempotencyKey: s.newKey(),
		Status:         domain.InvocationProposed,
	}
	// The admin opt-out: this kind executes without a human decision. The row is
	// born approved (no human decided it, so decided_by stays empty) and executed
	// below. The audit trail does not become optional because the approval did.
	if !cfg.RequiresApproval {
		inv.Status = domain.InvocationApproved
		inv.DecidedAt = &now
	}

	stored, created, err := s.repo.CreateInvocation(ctx, inv)
	if err != nil {
		return nil, fmt.Errorf("record proposal: %w", err)
	}

	// The proposal itself is audited by the propose_action tool's own decorator
	// (T-05) — this method is only ever reached through that tool. What is audited
	// here is the auto-execution, which no tool wraps.
	if !cfg.RequiresApproval && created && stored.Status == domain.InvocationApproved {
		kind, ref := s.turnActor(ctx)
		s.execute(ctx, stored, action, kind, ref)
		if refreshed, rErr := s.repo.GetInvocation(ctx, companyID, stored.ID); rErr == nil {
			stored = refreshed
		}
	}

	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "invocation_id": stored.ID, "action_kind": in.Kind,
		"status": stored.Status,
	}).Info("action proposed")

	return &tools.ProposeActionResult{
		InvocationID:     stored.ID,
		ActionKind:       stored.Kind,
		Status:           string(stored.Status),
		RequiresApproval: cfg.RequiresApproval,
		Description:      description,
		Message:          proposeMessage(cfg.RequiresApproval, description, stored),
	}, nil
}

func proposeMessage(requiresApproval bool, description string, inv *domain.ActionInvocation) string {
	if requiresApproval {
		return fmt.Sprintf("I've proposed this action and it needs approval before it runs: %s. "+
			"An admin can approve or reject it from the dashboard.", description)
	}
	switch inv.Status {
	case domain.InvocationExecuted:
		return fmt.Sprintf("Done: %s.", description)
	case domain.InvocationFailed:
		return fmt.Sprintf("I tried to do this and it failed: %s. %s", description, inv.ErrorText)
	default:
		return fmt.Sprintf("I've recorded this action: %s.", description)
	}
}

// --- approve / reject (human path, reached from T-11's endpoints) ---

// Approve approves a proposal and, if this call is the one that transitioned it,
// executes it. decidedBy is the approving user. Idempotent: approving an
// already-executed proposal returns it unchanged and runs nothing. A proposal
// past its TTL is refused with ErrActionExpired.
func (s *ActionService) Approve(ctx context.Context, companyID, id, decidedBy string) (*domain.ActionInvocation, error) {
	now := s.now()
	inv, transitioned, err := s.repo.Approve(ctx, companyID, id, decidedBy, now, now.Add(-actionProposalTTL))
	if err != nil {
		return inv, err
	}
	if !transitioned {
		// Someone else already approved (and perhaps executed) it. Nothing to do,
		// and nothing to double-audit.
		return inv, nil
	}

	action, ok := s.registry.Get(inv.Kind)
	if !ok {
		// Approved for a kind this deployment no longer runs. Fail it closed rather
		// than leave it stuck approved, and record why.
		if mErr := s.repo.MarkFailed(ctx, companyID, id, "action kind no longer available", now); mErr != nil {
			logrus.WithError(mErr).WithField("invocation_id", id).Warn("could not mark unavailable action failed")
		}
		s.auditDecision(ctx, inv, actionToolApprove, domain.ActorKindUser, decidedBy, domain.ActionStatusError, "action kind no longer available")
		return s.reload(ctx, companyID, id, inv), fmt.Errorf("%w: action %q is no longer available", domain.ErrInvalidInput, inv.Kind)
	}

	// The decision is a human's; record it under that human's authority.
	s.auditDecision(ctx, inv, actionToolApprove, domain.ActorKindUser, decidedBy, domain.ActionStatusOK, "")
	s.execute(ctx, inv, action, domain.ActorKindUser, decidedBy)
	return s.reload(ctx, companyID, id, inv), nil
}

// Reject terminates a proposal. Idempotent, and it never has a side effect — the
// action is not run, and nothing but the row's own status changes.
func (s *ActionService) Reject(ctx context.Context, companyID, id, decidedBy string) (*domain.ActionInvocation, error) {
	now := s.now()
	inv, err := s.repo.Reject(ctx, companyID, id, decidedBy, now)
	if err != nil {
		return inv, err
	}
	s.auditDecision(ctx, inv, actionToolReject, domain.ActorKindUser, decidedBy, domain.ActionStatusOK, "")
	return inv, nil
}

// --- company_actions configuration (admin, T-11) ---

// AvailableKinds is every action kind this deployment can run, sorted. The
// Settings surface offers these as the kinds an admin may enable — a kind the
// registry does not hold cannot be turned on, because nothing could execute it.
func (s *ActionService) AvailableKinds() []string {
	return s.registry.Kinds()
}

// ActionCatalogEntry is one kind a company has enabled, as a turn is told about
// it (T-12b follow-up).
type ActionCatalogEntry struct {
	Kind             string
	Usage            string
	RequiresApproval bool
	// Options are the names this kind's params may reference — http_action's
	// registered endpoints. Empty for a kind with no per-tenant vocabulary, and
	// empty for a tenant who has registered none, which reads differently in the
	// prompt: "you have none registered" rather than "pick one".
	Options []string
}

// CatalogForTurn is what a company has actually enabled, with each kind's
// parameter contract and the names it may reference.
//
// It exists because `T-12b` shipped a capability a tenant could enable,
// configure, and never reach: `propose_action`'s description names send_message
// as its example and spells out that action's parameters, so an agent asked to
// file a ticket had no way to learn that `http_action` was on or that an
// endpoint called `ops_ticket` existed. In the 2026-08-02 gate, four turns tried
// and one succeeded — the one whose user message dictated the tool arguments.
//
// Enabled kinds only. A kind an admin has not turned on is one the propose path
// refuses, and telling a turn about it would spend context on a refusal.
func (s *ActionService) CatalogForTurn(ctx context.Context, companyID string) ([]ActionCatalogEntry, error) {
	cfgs, err := s.repo.ListCompanyActions(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]ActionCatalogEntry, 0, len(cfgs))
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		action, ok := s.registry.Get(cfg.Kind)
		if !ok {
			// Enabled in the database, absent from this build. The propose path
			// already refuses it; a turn does not need to hear about it.
			continue
		}
		entry := ActionCatalogEntry{
			Kind:             cfg.Kind,
			Usage:            action.Usage(),
			RequiresApproval: cfg.RequiresApproval,
		}
		if opt, ok := action.(actions.Optioner); ok {
			names, err := opt.TurnOptions(ctx)
			if err != nil {
				// A catalog is a hint. Losing the names costs the turn a guess;
				// losing the turn costs the answer.
				logrus.WithError(err).WithFields(logrus.Fields{
					"company_id": companyID, "action_kind": cfg.Kind,
				}).Warn("action options lookup failed; the turn is told the kind without its names")
			}
			entry.Options = names
		}
		out = append(out, entry)
	}
	return out, nil
}

// ListConfig returns a company's per-kind configuration — what is enabled,
// whether it still needs approval, and who may decide it.
func (s *ActionService) ListConfig(ctx context.Context, companyID string) ([]*domain.CompanyAction, error) {
	return s.repo.ListCompanyActions(ctx, companyID)
}

// ActionConfigInput is the admin-editable shape of one kind's configuration. It
// deliberately excludes config_encrypted: send_message needs none, and the
// encrypted-credential plumbing an http_action needs (T-12b) travels a separate
// path that holds the DSN cipher, never a JSON body an admin PUTs.
type ActionConfigInput struct {
	Enabled          bool     `json:"enabled"`
	RequiresApproval bool     `json:"requires_approval"`
	AllowedRoles     []string `json:"allowed_roles"`
}

// ConfigureAction enables or reconfigures a kind for a company (admin). It
// refuses a kind the deployment cannot run — enabling send_message on a build
// that has no such action would let the agent propose something nothing can
// carry out. Turning approval off is permitted here because the endpoint is
// admin-only; it stays an explicit choice, never a default.
func (s *ActionService) ConfigureAction(ctx context.Context, companyID, kind, actorID string, in ActionConfigInput) (*domain.CompanyAction, error) {
	if _, ok := s.registry.Get(kind); !ok {
		return nil, fmt.Errorf("%w: no action named %q is available on this deployment", domain.ErrInvalidInput, kind)
	}
	roles := in.AllowedRoles
	if roles == nil {
		roles = []string{}
	}
	cfg := &domain.CompanyAction{
		CompanyID:        companyID,
		Kind:             kind,
		Enabled:          in.Enabled,
		RequiresApproval: in.RequiresApproval,
		AllowedRoles:     roles,
		CreatedBy:        actorID,
	}
	if err := s.repo.UpsertCompanyAction(ctx, cfg); err != nil {
		return nil, fmt.Errorf("configure action: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "action_kind": kind,
		"enabled": in.Enabled, "requires_approval": in.RequiresApproval,
	}).Info("action configured")
	return s.repo.GetCompanyAction(ctx, companyID, kind)
}

// --- reads (for T-11's endpoints and the ledger view) ---

// Get returns one invocation, company-scoped.
func (s *ActionService) Get(ctx context.Context, companyID, id string) (*domain.ActionInvocation, error) {
	return s.repo.GetInvocation(ctx, companyID, id)
}

// ListPending returns a company's proposals awaiting a decision.
func (s *ActionService) ListPending(ctx context.Context, companyID string) ([]*domain.ActionInvocation, error) {
	return s.repo.ListPending(ctx, companyID)
}

// PermittedToDecide reports whether a caller in role may approve or reject an
// invocation. The gate is the invocation's kind, not the invocation: a kind's
// company_actions.allowed_roles names who may decide it, and an empty list means
// any member — the migration's stated default. Enforced here rather than in the
// coarse role policy table because the permitted set is per company per kind, a
// shape a static route→role map cannot express (T-11). A kind whose config has
// gone missing is decided by admins only, the safe reading of an absent rule.
func (s *ActionService) PermittedToDecide(ctx context.Context, companyID, invocationID, role string) (bool, error) {
	inv, err := s.repo.GetInvocation(ctx, companyID, invocationID)
	if err != nil {
		return false, err
	}
	cfg, err := s.repo.GetCompanyAction(ctx, companyID, inv.Kind)
	if errors.Is(err, domain.ErrNotFound) {
		return role == string(domain.RoleAdmin), nil
	}
	if err != nil {
		return false, err
	}
	if len(cfg.AllowedRoles) == 0 {
		return true, nil
	}
	for _, r := range cfg.AllowedRoles {
		if r == role {
			return true, nil
		}
	}
	return false, nil
}

// List returns a company's invocations newest first.
func (s *ActionService) List(ctx context.Context, companyID string, limit, offset int) ([]*domain.ActionInvocation, error) {
	return s.repo.ListInvocations(ctx, companyID, limit, offset)
}

// --- execution ---

// execute runs an approved action and records the outcome. It is the ONLY caller
// of Action.Execute, and it is reached only after a proposal has been transitioned
// to approved — the tool cannot get here. actorKind/actorRef name who is
// accountable: the approving user, or the agent's turn actor on the auto path.
func (s *ActionService) execute(ctx context.Context, inv *domain.ActionInvocation, action actions.Action, actorKind domain.ActorKind, actorRef string) {
	now := s.now()
	result, err := action.Execute(ctx, inv.ParamsRedacted)
	if err != nil {
		if mErr := s.repo.MarkFailed(ctx, inv.CompanyID, inv.ID, err.Error(), now); mErr != nil {
			logrus.WithError(mErr).WithField("invocation_id", inv.ID).Warn("action failed and the failure was not recorded")
		}
		s.auditDecision(ctx, inv, actionToolExecute, actorKind, actorRef, domain.ActionStatusError, err.Error())
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": inv.CompanyID, "invocation_id": inv.ID, "action_kind": inv.Kind,
		}).Warn("action execution failed")
		return
	}
	if mErr := s.repo.MarkExecuted(ctx, inv.CompanyID, inv.ID, result, now); mErr != nil {
		// The action ran; only the record of it did not. Louder than a failed
		// action, because the world changed and the ledger disagrees.
		logrus.WithError(mErr).WithFields(logrus.Fields{
			"company_id": inv.CompanyID, "invocation_id": inv.ID,
		}).Error("action executed but the ledger was not updated")
	}
	s.auditDecision(ctx, inv, actionToolExecute, actorKind, actorRef, domain.ActionStatusOK, "")
}

// --- audit ---

// The audit rows a decision writes carry these names. They are not real tools —
// the audit log's tool_name column is text — but they put a proposal's whole life
// (propose_action from the decorator, then these) on one thread's audit timeline.
const (
	actionToolApprove = "action:approve"
	actionToolReject  = "action:reject"
	actionToolExecute = "action:execute"
)

// auditDecision writes one immutable row for a decision or an execution. It never
// fails the operation: a decision whose audit write failed is logged at Warn, the
// same trade-off the tool decorator makes, because the action mattering more than
// the record of it is exactly backwards for a log but exactly right for the user.
func (s *ActionService) auditDecision(ctx context.Context, inv *domain.ActionInvocation, tool string, actorKind domain.ActorKind, actorRef string, status domain.ActionStatus, errText string) {
	if s.audit == nil {
		return
	}
	args, _ := json.Marshal(map[string]string{"invocation_id": inv.ID, "action_kind": inv.Kind})
	sum := sha256.Sum256(append([]byte(tool+"|"), args...))
	row := &domain.AgentAction{
		CompanyID:    inv.CompanyID,
		ThreadID:     inv.ThreadID,
		MessageID:    inv.MessageID,
		ActorKind:    actorKind,
		ActorRef:     actorRef,
		Channel:      domain.Channel(tenantctx.Channel(ctx)),
		AgentID:      agentscope.AgentID(ctx),
		ToolName:     tool,
		ArgsRedacted: args,
		ArgsHash:     hex.EncodeToString(sum[:]),
		ResultStatus: status,
		ErrorText:    errText,
		RequestID:    tenantctx.RequestID(ctx),
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.audit.Create(writeCtx, row); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": inv.CompanyID, "invocation_id": inv.ID, "tool": tool,
		}).Warn("action decision not audited; the decision itself stood")
	}
}

// turnActor is who the current turn runs as, for auditing an auto-executed
// action. Defaults to user, like the tool decorator, when the turn set no actor.
func (s *ActionService) turnActor(ctx context.Context) (domain.ActorKind, string) {
	kind, ref := tenantctx.Actor(ctx)
	if kind == "" {
		return domain.ActorKindUser, ref
	}
	return domain.ActorKind(kind), ref
}

// reload returns the freshest view of an invocation, falling back to the one in
// hand if the re-read fails — a decision that succeeded should not report an error
// only because a subsequent read did.
func (s *ActionService) reload(ctx context.Context, companyID, id string, fallback *domain.ActionInvocation) *domain.ActionInvocation {
	if refreshed, err := s.repo.GetInvocation(ctx, companyID, id); err == nil {
		return refreshed
	}
	return fallback
}
