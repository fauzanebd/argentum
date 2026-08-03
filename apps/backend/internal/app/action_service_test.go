package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools"
)

// The state machine (T-10). These tests exercise the real ActionService against
// an in-memory ActionRepository that implements the exact transition contract
// domain.ActionRepository documents — proposed→approved wins once, expiry, the
// idempotent decisions. The SQL that implements the same contract under a row
// lock (action_repo.go) is the one thing left for the live gate; the policy the
// service layers on top is all here.

// --- fakes ---

type memInvocationRepo struct {
	mu     sync.Mutex
	cfg    map[string]*domain.CompanyAction    // company|kind -> config
	invs   map[string]*domain.ActionInvocation // id -> invocation
	byKey  map[string]string                   // company|key -> id
	nextID int
	// now stamps proposed_at, the way the real table's DEFAULT now() does. The
	// harness points it at the test clock so expiry is testable without sleeping.
	now func() time.Time
}

func newMemInvocationRepo() *memInvocationRepo {
	return &memInvocationRepo{
		cfg:   map[string]*domain.CompanyAction{},
		invs:  map[string]*domain.ActionInvocation{},
		byKey: map[string]string{},
		now:   time.Now,
	}
}

func ck(company, k string) string { return company + "|" + k }

func cloneInv(inv *domain.ActionInvocation) *domain.ActionInvocation {
	cp := *inv
	return &cp
}

// enable is a test helper that turns a kind on for a company.
func (r *memInvocationRepo) enable(company, kind string, requiresApproval bool) {
	r.cfg[ck(company, kind)] = &domain.CompanyAction{
		CompanyID: company, Kind: kind, Enabled: true, RequiresApproval: requiresApproval,
	}
}

func (r *memInvocationRepo) GetCompanyAction(_ context.Context, companyID, kind string) (*domain.CompanyAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.cfg[ck(companyID, kind)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (r *memInvocationRepo) ListCompanyActions(_ context.Context, companyID string) ([]*domain.CompanyAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.CompanyAction
	for _, a := range r.cfg {
		if a.CompanyID == companyID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *memInvocationRepo) UpsertCompanyAction(_ context.Context, a *domain.CompanyAction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *a
	if cp.ID == "" {
		cp.ID = "ca-" + a.Kind
	}
	r.cfg[ck(a.CompanyID, a.Kind)] = &cp
	a.ID = cp.ID
	return nil
}

func (r *memInvocationRepo) CreateInvocation(_ context.Context, inv *domain.ActionInvocation) (*domain.ActionInvocation, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.byKey[ck(inv.CompanyID, inv.IdempotencyKey)]; ok {
		return cloneInv(r.invs[id]), false, nil
	}
	r.nextID++
	stored := cloneInv(inv)
	stored.ID = "inv-" + string(rune('a'+r.nextID-1))
	if stored.Status == "" {
		stored.Status = domain.InvocationProposed
	}
	if stored.ProposedAt.IsZero() {
		stored.ProposedAt = r.now()
	}
	r.invs[stored.ID] = stored
	r.byKey[ck(inv.CompanyID, inv.IdempotencyKey)] = stored.ID
	return cloneInv(stored), true, nil
}

func (r *memInvocationRepo) GetInvocation(_ context.Context, companyID, id string) (*domain.ActionInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invs[id]
	if !ok || inv.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return cloneInv(inv), nil
}

func (r *memInvocationRepo) ListInvocations(_ context.Context, companyID string, _, _ int) ([]*domain.ActionInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.ActionInvocation
	for _, inv := range r.invs {
		if inv.CompanyID == companyID {
			out = append(out, cloneInv(inv))
		}
	}
	return out, nil
}

func (r *memInvocationRepo) ListPending(_ context.Context, companyID string) ([]*domain.ActionInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.ActionInvocation
	for _, inv := range r.invs {
		if inv.CompanyID == companyID && inv.Status == domain.InvocationProposed {
			out = append(out, cloneInv(inv))
		}
	}
	return out, nil
}

// Approve mirrors action_repo.go's contract exactly.
func (r *memInvocationRepo) Approve(_ context.Context, companyID, id, decidedBy string, now, expireBefore time.Time) (*domain.ActionInvocation, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invs[id]
	if !ok || inv.CompanyID != companyID {
		return nil, false, domain.ErrNotFound
	}
	switch inv.Status {
	case domain.InvocationProposed:
		if inv.ProposedAt.Before(expireBefore) {
			inv.Status = domain.InvocationExpired
			return cloneInv(inv), false, domain.ErrActionExpired
		}
		inv.Status = domain.InvocationApproved
		inv.DecidedAt = &now
		inv.DecidedBy = decidedBy
		return cloneInv(inv), true, nil
	case domain.InvocationApproved, domain.InvocationExecuted, domain.InvocationFailed:
		return cloneInv(inv), false, nil
	case domain.InvocationRejected:
		return cloneInv(inv), false, domain.ErrConflict
	case domain.InvocationExpired:
		return cloneInv(inv), false, domain.ErrActionExpired
	default:
		return cloneInv(inv), false, domain.ErrConflict
	}
}

func (r *memInvocationRepo) Reject(_ context.Context, companyID, id, decidedBy string, now time.Time) (*domain.ActionInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invs[id]
	if !ok || inv.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	switch inv.Status {
	case domain.InvocationProposed:
		inv.Status = domain.InvocationRejected
		inv.DecidedAt = &now
		inv.DecidedBy = decidedBy
		return cloneInv(inv), nil
	case domain.InvocationRejected:
		return cloneInv(inv), nil
	default:
		return cloneInv(inv), domain.ErrConflict
	}
}

func (r *memInvocationRepo) MarkExecuted(_ context.Context, companyID, id string, result json.RawMessage, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invs[id]
	if !ok || inv.CompanyID != companyID || inv.Status != domain.InvocationApproved {
		return domain.ErrConflict
	}
	inv.Status = domain.InvocationExecuted
	inv.ExecutedAt = &now
	inv.Result = result
	inv.ErrorText = ""
	return nil
}

func (r *memInvocationRepo) MarkFailed(_ context.Context, companyID, id, errText string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invs[id]
	if !ok || inv.CompanyID != companyID || inv.Status != domain.InvocationApproved {
		return domain.ErrConflict
	}
	inv.Status = domain.InvocationFailed
	inv.ExecutedAt = &now
	inv.ErrorText = errText
	return nil
}

// memDecisionAudit records the decision rows the service writes.
type memDecisionAudit struct {
	mu   sync.Mutex
	rows []*domain.AgentAction
}

func (a *memDecisionAudit) Create(_ context.Context, row *domain.AgentAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := *row
	a.rows = append(a.rows, &cp)
	return nil
}

func (a *memDecisionAudit) countTool(name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, r := range a.rows {
		if r.ToolName == name {
			n++
		}
	}
	return n
}

// stubAction is a concrete action that counts executions and can be told to fail.
// options / optionsErr drive the actions.Optioner half, which only a kind with a
// per-tenant vocabulary implements — set them and the stub answers as
// http_action does.
type stubAction struct {
	kind        string
	validateErr error
	executeErr  error
	execCount   int
	lastParams  json.RawMessage
	options     []string
	optionsErr  error
}

func (a *stubAction) Kind() string                               { return a.kind }
func (a *stubAction) Describe(_ json.RawMessage) (string, error) { return "send a test message", nil }
func (a *stubAction) Validate(_ json.RawMessage) error           { return a.validateErr }
func (a *stubAction) Usage() string                              { return "params: {\"body\": \"<text>\"}" }
func (a *stubAction) TurnOptions(context.Context) ([]string, error) {
	return a.options, a.optionsErr
}
func (a *stubAction) Execute(_ context.Context, p json.RawMessage) (json.RawMessage, error) {
	a.execCount++
	a.lastParams = p
	if a.executeErr != nil {
		return nil, a.executeErr
	}
	return json.RawMessage(`{"delivered":true}`), nil
}

// actionHarness wires a service with a controllable clock.
type actionHarness struct {
	svc   *ActionService
	repo  *memInvocationRepo
	audit *memDecisionAudit
	act   *stubAction
	clock time.Time
}

func newActionHarness(t *testing.T, requiresApproval bool) *actionHarness {
	t.Helper()
	repo := newMemInvocationRepo()
	audit := &memDecisionAudit{}
	act := &stubAction{kind: "send_message"}
	svc := NewActionService(repo, actions.NewRegistry(act), audit)
	h := &actionHarness{svc: svc, repo: repo, audit: audit, act: act, clock: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	svc.now = func() time.Time { return h.clock }
	repo.now = func() time.Time { return h.clock }
	keyN := 0
	svc.newKey = func() string { keyN++; return "key-" + string(rune('a'+keyN-1)) }
	repo.enable("co-1", "send_message", requiresApproval)
	return h
}

func (h *actionHarness) ctx() context.Context {
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	ctx = tenantctx.WithThreadID(ctx, "th-1")
	ctx = tenantctx.WithMessageID(ctx, "msg-1")
	return ctx
}

func (h *actionHarness) propose(t *testing.T) *tools.ProposeActionResult {
	t.Helper()
	res, err := h.svc.ProposeAction(h.ctx(), tools.ProposeActionInput{
		Kind: "send_message", Params: json.RawMessage(`{"channel":"whatsapp","target_ref":"+62","body":"hi"}`),
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	return res
}

// --- tests ---

// The agent can propose, and proposing runs nothing.
func TestPropose_RequiresApproval_DoesNotExecute(t *testing.T) {
	h := newActionHarness(t, true)
	res := h.propose(t)

	if res.Status != string(domain.InvocationProposed) {
		t.Fatalf("status = %q, want proposed", res.Status)
	}
	if !res.RequiresApproval {
		t.Fatal("RequiresApproval = false, want true")
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d, want 0 — a proposal must not execute", h.act.execCount)
	}
}

// A kind the company has not enabled cannot be proposed.
func TestPropose_NotEnabled_Refused(t *testing.T) {
	h := newActionHarness(t, true)
	// A different company has nothing enabled.
	ctx := tenantctx.WithCompanyID(context.Background(), "co-2")
	_, err := h.svc.ProposeAction(ctx, tools.ProposeActionInput{
		Kind: "send_message", Params: json.RawMessage(`{}`),
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d, want 0", h.act.execCount)
	}
}

// An unknown action kind is refused before anything is written.
func TestPropose_UnknownKind_Refused(t *testing.T) {
	h := newActionHarness(t, true)
	h.repo.enable("co-1", "http_action", true) // enabled but not registered
	_, err := h.svc.ProposeAction(h.ctx(), tools.ProposeActionInput{
		Kind: "http_action", Params: json.RawMessage(`{}`),
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// Approving executes exactly once; approving twice does not double-execute.
func TestApprove_ExecutesOnce_DoubleApproveNoDoubleExecute(t *testing.T) {
	h := newActionHarness(t, true)
	res := h.propose(t)

	inv, err := h.svc.Approve(h.ctx(), "co-1", res.InvocationID, "user-9")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if inv.Status != domain.InvocationExecuted {
		t.Fatalf("status = %q, want executed", inv.Status)
	}
	if h.act.execCount != 1 {
		t.Fatalf("execCount = %d, want 1", h.act.execCount)
	}

	// Second approval: idempotent, no second execution.
	inv2, err := h.svc.Approve(h.ctx(), "co-1", res.InvocationID, "user-9")
	if err != nil {
		t.Fatalf("second approve: %v", err)
	}
	if inv2.Status != domain.InvocationExecuted {
		t.Fatalf("second approve status = %q, want executed", inv2.Status)
	}
	if h.act.execCount != 1 {
		t.Fatalf("execCount = %d after double approve, want 1 — double execution", h.act.execCount)
	}
}

// Rejecting leaves no side effect, and an approve after a reject is refused.
func TestReject_NoSideEffect(t *testing.T) {
	h := newActionHarness(t, true)
	res := h.propose(t)

	inv, err := h.svc.Reject(h.ctx(), "co-1", res.InvocationID, "user-9")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if inv.Status != domain.InvocationRejected {
		t.Fatalf("status = %q, want rejected", inv.Status)
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d, want 0", h.act.execCount)
	}

	if _, err := h.svc.Approve(h.ctx(), "co-1", res.InvocationID, "user-9"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("approve after reject err = %v, want ErrConflict", err)
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d after approve-of-rejected, want 0", h.act.execCount)
	}
}

// A proposal older than the 24h window cannot be approved.
func TestApprove_ExpiredCannotBeApproved(t *testing.T) {
	h := newActionHarness(t, true)
	res := h.propose(t)

	// Move the clock past the TTL.
	h.clock = h.clock.Add(actionProposalTTL + time.Hour)

	inv, err := h.svc.Approve(h.ctx(), "co-1", res.InvocationID, "user-9")
	if !errors.Is(err, domain.ErrActionExpired) {
		t.Fatalf("err = %v, want ErrActionExpired", err)
	}
	if inv != nil && inv.Status != domain.InvocationExpired {
		t.Fatalf("status = %q, want expired", inv.Status)
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d, want 0 — an expired proposal must not run", h.act.execCount)
	}
}

// Every decision appears in agent_actions.
func TestDecisionsAreAudited(t *testing.T) {
	h := newActionHarness(t, true)

	// Approve path: an approve row and an execute row.
	res := h.propose(t)
	if _, err := h.svc.Approve(h.ctx(), "co-1", res.InvocationID, "user-9"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := h.audit.countTool("action:approve"); got != 1 {
		t.Fatalf("action:approve rows = %d, want 1", got)
	}
	if got := h.audit.countTool("action:execute"); got != 1 {
		t.Fatalf("action:execute rows = %d, want 1", got)
	}

	// Reject path: a reject row, on a fresh proposal.
	res2 := h.propose(t)
	if _, err := h.svc.Reject(h.ctx(), "co-1", res2.InvocationID, "user-9"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got := h.audit.countTool("action:reject"); got != 1 {
		t.Fatalf("action:reject rows = %d, want 1", got)
	}

	// Decision rows carry the thread the proposal was raised in, and the human's
	// authority — not the agent's.
	for _, row := range h.audit.rows {
		if row.ThreadID != "th-1" {
			t.Fatalf("audit row thread = %q, want th-1", row.ThreadID)
		}
		if row.ActorKind != domain.ActorKindUser || row.ActorRef != "user-9" {
			t.Fatalf("audit actor = %s/%s, want user/user-9", row.ActorKind, row.ActorRef)
		}
		if row.ArgsHash == "" {
			t.Fatal("audit row has empty args_hash (NOT NULL column)")
		}
	}
}

// The admin opt-out (requires_approval=false) executes on propose and audits it.
func TestPropose_AutoExecuteWhenApprovalNotRequired(t *testing.T) {
	h := newActionHarness(t, false)
	res := h.propose(t)

	if res.RequiresApproval {
		t.Fatal("RequiresApproval = true, want false")
	}
	if res.Status != string(domain.InvocationExecuted) {
		t.Fatalf("status = %q, want executed", res.Status)
	}
	if h.act.execCount != 1 {
		t.Fatalf("execCount = %d, want 1", h.act.execCount)
	}
	if got := h.audit.countTool("action:execute"); got != 1 {
		t.Fatalf("action:execute rows = %d, want 1", got)
	}
}

// A failing action lands the invocation in failed with the error recorded.
func TestApprove_ExecutionFailureIsRecorded(t *testing.T) {
	h := newActionHarness(t, true)
	h.act.executeErr = errors.New("channel unreachable")
	res := h.propose(t)

	inv, err := h.svc.Approve(h.ctx(), "co-1", res.InvocationID, "user-9")
	if err != nil {
		t.Fatalf("approve returned err = %v; an execution failure is recorded, not returned", err)
	}
	if inv.Status != domain.InvocationFailed {
		t.Fatalf("status = %q, want failed", inv.Status)
	}
	if inv.ErrorText == "" {
		t.Fatal("error_text empty, want the failure reason")
	}
	if got := h.audit.countTool("action:execute"); got != 1 {
		t.Fatalf("action:execute rows = %d, want 1", got)
	}
}

// A decision on another company's invocation is a not-found, never a cross-tenant act.
func TestApprove_CrossTenantIsNotFound(t *testing.T) {
	h := newActionHarness(t, true)
	res := h.propose(t)

	if _, err := h.svc.Approve(h.ctx(), "co-OTHER", res.InvocationID, "user-9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant approve err = %v, want ErrNotFound", err)
	}
	if h.act.execCount != 0 {
		t.Fatalf("execCount = %d, want 0", h.act.execCount)
	}
}

// --- the turn-time catalog (T-12b follow-up) ---

// Only what an admin turned on. A kind in the registry but not enabled is one
// the propose path refuses, so naming it in a turn would spend context on a
// refusal.
func TestCatalogForTurn_ListsOnlyEnabledKinds(t *testing.T) {
	repo := newMemInvocationRepo()
	enabled := &stubAction{kind: "send_message"}
	offKind := &stubAction{kind: "http_action"}
	svc := NewActionService(repo, actions.NewRegistry(enabled, offKind), &memDecisionAudit{})
	repo.enable("co-1", "send_message", true)

	entries, err := svc.CatalogForTurn(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "send_message" {
		t.Fatalf("entries = %+v, want only send_message", entries)
	}
	if entries[0].Usage == "" {
		t.Error("usage is empty — the turn is told a kind exists and not how to call it")
	}
	if !entries[0].RequiresApproval {
		t.Error("requires_approval lost")
	}
}

// The half that cannot live in a static tool description: the names this tenant
// registered. http_action is the kind that has them.
func TestCatalogForTurn_CarriesTheTenantsOwnNames(t *testing.T) {
	repo := newMemInvocationRepo()
	act := &stubAction{kind: "http_action", options: []string{"ops_ticket", "refund_request"}}
	svc := NewActionService(repo, actions.NewRegistry(act), &memDecisionAudit{})
	repo.enable("co-1", "http_action", true)

	entries, err := svc.CatalogForTurn(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got := entries[0].Options; len(got) != 2 || got[0] != "ops_ticket" {
		t.Errorf("options = %v, want the registered endpoint names", got)
	}
}

// A names lookup that fails degrades to the kind without its names. The turn
// can still propose — it just has to be told the name by the user, which is
// exactly where this feature was before the catalog existed.
func TestCatalogForTurn_AnOptionsFailureStillListsTheKind(t *testing.T) {
	repo := newMemInvocationRepo()
	act := &stubAction{kind: "http_action", optionsErr: errors.New("endpoint store is down")}
	svc := NewActionService(repo, actions.NewRegistry(act), &memDecisionAudit{})
	repo.enable("co-1", "http_action", true)

	entries, err := svc.CatalogForTurn(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("catalog must not fail on an options error: %v", err)
	}
	if len(entries) != 1 || len(entries[0].Options) != 0 {
		t.Fatalf("entries = %+v, want the kind with no names", entries)
	}
}
