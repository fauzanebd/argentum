package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ActionRepo stores the action framework's switchboard and ledger (T-10).
//
// Company-scoped on every read where the id arrived from a request. The
// exactly-once guarantee lives in Approve: the proposed→approved transition runs
// under SELECT ... FOR UPDATE so that, of two requests racing to approve one
// proposal, exactly one is told it may execute.
type ActionRepo struct{ db *sql.DB }

func NewActionRepo(db *sql.DB) *ActionRepo { return &ActionRepo{db: db} }

// --- company_actions ---

const companyActionColumns = `id, company_id, action_kind, enabled, requires_approval,
	config_encrypted, allowed_roles, COALESCE(created_by::text, ''), created_at, updated_at`

func scanCompanyAction(row interface {
	Scan(dest ...interface{}) error
}) (*domain.CompanyAction, error) {
	a := &domain.CompanyAction{}
	var roles []byte
	if err := row.Scan(
		&a.ID, &a.CompanyID, &a.Kind, &a.Enabled, &a.RequiresApproval,
		&a.ConfigEncrypted, &roles, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(roles) > 0 {
		if err := json.Unmarshal(roles, &a.AllowedRoles); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_roles: %w", err)
		}
	}
	if a.AllowedRoles == nil {
		a.AllowedRoles = []string{}
	}
	return a, nil
}

func (r *ActionRepo) GetCompanyAction(ctx context.Context, companyID, kind string) (*domain.CompanyAction, error) {
	q := `SELECT ` + companyActionColumns + ` FROM company_actions WHERE company_id = $1 AND action_kind = $2`
	a, err := scanCompanyAction(r.db.QueryRowContext(ctx, q, companyID, kind))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return a, err
}

func (r *ActionRepo) ListCompanyActions(ctx context.Context, companyID string) ([]*domain.CompanyAction, error) {
	q := `SELECT ` + companyActionColumns + ` FROM company_actions WHERE company_id = $1 ORDER BY action_kind`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("list company actions: %w", err)
	}
	defer rows.Close()
	out := []*domain.CompanyAction{}
	for rows.Next() {
		a, err := scanCompanyAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertCompanyAction enables or reconfigures a kind, keyed on (company_id,
// action_kind). created_by and config_encrypted are set on insert and preserved
// on update unless supplied — an admin toggling requires_approval must not have
// to re-supply the credentials.
func (r *ActionRepo) UpsertCompanyAction(ctx context.Context, a *domain.CompanyAction) error {
	roles, err := json.Marshal(nonNilStrings(a.AllowedRoles))
	if err != nil {
		return fmt.Errorf("marshal allowed_roles: %w", err)
	}
	const q = `
		INSERT INTO company_actions (
			company_id, action_kind, enabled, requires_approval, config_encrypted, allowed_roles, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, NULLIF($7, '')::uuid
		)
		ON CONFLICT (company_id, action_kind) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			requires_approval = EXCLUDED.requires_approval,
			config_encrypted = COALESCE(EXCLUDED.config_encrypted, company_actions.config_encrypted),
			allowed_roles = EXCLUDED.allowed_roles,
			updated_at = now()
		RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, q,
		a.CompanyID, a.Kind, a.Enabled, a.RequiresApproval, a.ConfigEncrypted, roles, a.CreatedBy,
	).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

// --- action_invocations ---

const invocationColumns = `id, company_id, COALESCE(thread_id::text, ''), COALESCE(message_id::text, ''),
	action_kind, params_redacted, idempotency_key, status, proposed_at, decided_at,
	COALESCE(decided_by::text, ''), executed_at, result, COALESCE(error_text, '')`

func scanInvocation(row interface {
	Scan(dest ...interface{}) error
}) (*domain.ActionInvocation, error) {
	inv := &domain.ActionInvocation{}
	var status string
	var decidedAt, executedAt sql.NullTime
	var result []byte
	if err := row.Scan(
		&inv.ID, &inv.CompanyID, &inv.ThreadID, &inv.MessageID,
		&inv.Kind, &inv.ParamsRedacted, &inv.IdempotencyKey, &status, &inv.ProposedAt, &decidedAt,
		&inv.DecidedBy, &executedAt, &result, &inv.ErrorText,
	); err != nil {
		return nil, err
	}
	inv.Status = domain.InvocationStatus(status)
	if decidedAt.Valid {
		v := decidedAt.Time
		inv.DecidedAt = &v
	}
	if executedAt.Valid {
		v := executedAt.Time
		inv.ExecutedAt = &v
	}
	if len(result) > 0 {
		inv.Result = json.RawMessage(result)
	}
	return inv, nil
}

// CreateInvocation inserts a proposal idempotently. ON CONFLICT DO NOTHING makes
// a repeated (company_id, idempotency_key) a no-op; the RETURNING then comes back
// empty, so a miss is read as "already exists" and the stored row is fetched and
// returned with created=false.
func (r *ActionRepo) CreateInvocation(ctx context.Context, inv *domain.ActionInvocation) (*domain.ActionInvocation, bool, error) {
	params := inv.ParamsRedacted
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	status := inv.Status
	if status == "" {
		status = domain.InvocationProposed
	}
	const q = `
		INSERT INTO action_invocations (
			company_id, thread_id, message_id, action_kind, params_redacted, idempotency_key, status,
			decided_at, decided_by, executed_at, result
		) VALUES (
			$1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5::jsonb, $6, $7,
			$8, NULLIF($9, '')::uuid, $10, $11
		)
		ON CONFLICT (company_id, idempotency_key) DO NOTHING
		RETURNING ` + invocationColumns
	stored, err := scanInvocation(r.db.QueryRowContext(ctx, q,
		inv.CompanyID, inv.ThreadID, inv.MessageID, inv.Kind, string(params), inv.IdempotencyKey, string(status),
		inv.DecidedAt, inv.DecidedBy, inv.ExecutedAt, nullableJSON(inv.Result),
	))
	if errors.Is(err, sql.ErrNoRows) {
		// Conflict: a proposal with this key already exists. Return it as-is.
		existing, gErr := r.getInvocationByKey(ctx, inv.CompanyID, inv.IdempotencyKey)
		if gErr != nil {
			return nil, false, gErr
		}
		return existing, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert action invocation: %w", err)
	}
	return stored, true, nil
}

func (r *ActionRepo) getInvocationByKey(ctx context.Context, companyID, key string) (*domain.ActionInvocation, error) {
	q := `SELECT ` + invocationColumns + ` FROM action_invocations WHERE company_id = $1 AND idempotency_key = $2`
	inv, err := scanInvocation(r.db.QueryRowContext(ctx, q, companyID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return inv, err
}

func (r *ActionRepo) GetInvocation(ctx context.Context, companyID, id string) (*domain.ActionInvocation, error) {
	q := `SELECT ` + invocationColumns + ` FROM action_invocations WHERE company_id = $1 AND id = $2`
	inv, err := scanInvocation(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return inv, err
}

func (r *ActionRepo) ListInvocations(ctx context.Context, companyID string, limit, offset int) ([]*domain.ActionInvocation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + invocationColumns + ` FROM action_invocations
		WHERE company_id = $1 ORDER BY proposed_at DESC, id DESC LIMIT $2 OFFSET $3`
	return r.queryInvocations(ctx, q, companyID, limit, offset)
}

func (r *ActionRepo) ListPending(ctx context.Context, companyID string) ([]*domain.ActionInvocation, error) {
	q := `SELECT ` + invocationColumns + ` FROM action_invocations
		WHERE company_id = $1 AND status = 'proposed' ORDER BY proposed_at DESC`
	return r.queryInvocations(ctx, q, companyID)
}

func (r *ActionRepo) queryInvocations(ctx context.Context, q string, args ...interface{}) ([]*domain.ActionInvocation, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list action invocations: %w", err)
	}
	defer rows.Close()
	out := []*domain.ActionInvocation{}
	for rows.Next() {
		inv, err := scanInvocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// Approve moves a proposal to approved, under a row lock so exactly one racing
// caller wins. transitioned is true only for that caller; everyone else gets the
// row's current state and transitioned=false, and must not execute. See
// domain.ActionRepository.Approve for the full contract.
func (r *ActionRepo) Approve(
	ctx context.Context, companyID, id, decidedBy string, now, expireBefore time.Time,
) (*domain.ActionInvocation, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin approve tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	lock := `SELECT ` + invocationColumns + ` FROM action_invocations
		WHERE company_id = $1 AND id = $2 FOR UPDATE`
	inv, err := scanInvocation(tx.QueryRowContext(ctx, lock, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, domain.ErrNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock invocation: %w", err)
	}

	switch inv.Status {
	case domain.InvocationProposed:
		if inv.ProposedAt.Before(expireBefore) {
			if _, err := tx.ExecContext(ctx,
				`UPDATE action_invocations SET status = 'expired' WHERE id = $1`, inv.ID); err != nil {
				return nil, false, fmt.Errorf("expire invocation: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return nil, false, fmt.Errorf("commit expire: %w", err)
			}
			inv.Status = domain.InvocationExpired
			return inv, false, domain.ErrActionExpired
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE action_invocations SET status = 'approved', decided_at = $2, decided_by = NULLIF($3, '')::uuid
			 WHERE id = $1`, inv.ID, now, decidedBy); err != nil {
			return nil, false, fmt.Errorf("approve invocation: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit approve: %w", err)
		}
		inv.Status = domain.InvocationApproved
		inv.DecidedAt = &now
		inv.DecidedBy = decidedBy
		return inv, true, nil

	case domain.InvocationApproved, domain.InvocationExecuted, domain.InvocationFailed:
		// Already decided in our favour: idempotent, and not ours to execute. The
		// caller checks Status to decide whether anything remains to do.
		_ = tx.Commit()
		return inv, false, nil

	case domain.InvocationRejected:
		_ = tx.Commit()
		return inv, false, domain.ErrConflict

	case domain.InvocationExpired:
		_ = tx.Commit()
		return inv, false, domain.ErrActionExpired

	default:
		_ = tx.Commit()
		return inv, false, domain.ErrConflict
	}
}

// Reject moves a proposal to rejected. Idempotent from rejected; ErrConflict from
// any state where a rejection would contradict what already happened.
func (r *ActionRepo) Reject(
	ctx context.Context, companyID, id, decidedBy string, now time.Time,
) (*domain.ActionInvocation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin reject tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	lock := `SELECT ` + invocationColumns + ` FROM action_invocations
		WHERE company_id = $1 AND id = $2 FOR UPDATE`
	inv, err := scanInvocation(tx.QueryRowContext(ctx, lock, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock invocation: %w", err)
	}

	switch inv.Status {
	case domain.InvocationProposed:
		if _, err := tx.ExecContext(ctx,
			`UPDATE action_invocations SET status = 'rejected', decided_at = $2, decided_by = NULLIF($3, '')::uuid
			 WHERE id = $1`, inv.ID, now, decidedBy); err != nil {
			return nil, fmt.Errorf("reject invocation: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit reject: %w", err)
		}
		inv.Status = domain.InvocationRejected
		inv.DecidedAt = &now
		inv.DecidedBy = decidedBy
		return inv, nil
	case domain.InvocationRejected:
		_ = tx.Commit()
		return inv, nil // idempotent
	default:
		_ = tx.Commit()
		return inv, domain.ErrConflict
	}
}

// MarkExecuted / MarkFailed record the outcome, but only for a row still
// approved. The WHERE status = 'approved' is the last guard: a second executor
// that somehow reaches here after the first finished finds nothing to update.
func (r *ActionRepo) MarkExecuted(ctx context.Context, companyID, id string, result json.RawMessage, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE action_invocations SET status = 'executed', executed_at = $3, result = $4, error_text = NULL
		 WHERE company_id = $1 AND id = $2 AND status = 'approved'`,
		companyID, id, now, nullableJSON(result))
	if err != nil {
		return fmt.Errorf("mark executed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (r *ActionRepo) MarkFailed(ctx context.Context, companyID, id, errText string, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE action_invocations SET status = 'failed', executed_at = $3, error_text = NULLIF($4, '')
		 WHERE company_id = $1 AND id = $2 AND status = 'approved'`,
		companyID, id, now, errText)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrConflict
	}
	return nil
}

// nullableJSON turns an empty raw message into a real SQL NULL, so an invocation
// with no result stores NULL rather than the four bytes "null".
func nullableJSON(m json.RawMessage) interface{} {
	if len(m) == 0 {
		return nil
	}
	return []byte(m)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
