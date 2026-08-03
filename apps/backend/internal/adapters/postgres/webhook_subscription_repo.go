package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// WebhookSubscriptionRepo persists standing event subscriptions (T-15).
type WebhookSubscriptionRepo struct{ db *sql.DB }

func NewWebhookSubscriptionRepo(db *sql.DB) *WebhookSubscriptionRepo {
	return &WebhookSubscriptionRepo{db: db}
}

const webhookSubscriptionColumns = `
	id, company_id, url, events, enabled, consecutive_failures, disabled_reason,
	last_success_at, last_failure_at, created_at, updated_at`

func scanWebhookSubscription(s interface{ Scan(...any) error }) (*domain.WebhookSubscription, error) {
	sub := &domain.WebhookSubscription{}
	var events pq.StringArray
	var success, failure sql.NullTime
	if err := s.Scan(
		&sub.ID, &sub.CompanyID, &sub.URL, &events, &sub.Enabled,
		&sub.ConsecutiveFailures, &sub.DisabledReason, &success, &failure,
		&sub.CreatedAt, &sub.UpdatedAt,
	); err != nil {
		return nil, err
	}
	sub.Events = []string(events)
	if success.Valid {
		t := success.Time
		sub.LastSuccessAt = &t
	}
	if failure.Valid {
		t := failure.Time
		sub.LastFailureAt = &t
	}
	return sub, nil
}

func (r *WebhookSubscriptionRepo) Create(ctx context.Context, s *domain.WebhookSubscription) error {
	const q = `
		INSERT INTO webhook_subscriptions (company_id, url, events, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`
	return r.db.QueryRowContext(ctx, q, s.CompanyID, s.URL, pq.Array(s.Events), s.Enabled).
		Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
}

func (r *WebhookSubscriptionRepo) GetByID(ctx context.Context, companyID, id string) (*domain.WebhookSubscription, error) {
	q := `SELECT ` + webhookSubscriptionColumns + ` FROM webhook_subscriptions WHERE company_id = $1 AND id = $2`
	sub, err := scanWebhookSubscription(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sub, err
}

func (r *WebhookSubscriptionRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.WebhookSubscription, error) {
	q := `SELECT ` + webhookSubscriptionColumns + `
		FROM webhook_subscriptions WHERE company_id = $1 ORDER BY created_at`
	return r.querySubscriptions(ctx, q, companyID)
}

// ListForEvent is the fan-out's only read. `events @> ARRAY[$2]` uses the array
// containment operator rather than unnesting, so one index-friendly predicate
// answers it on the hot path of every watcher breach.
func (r *WebhookSubscriptionRepo) ListForEvent(ctx context.Context, companyID, event string) ([]*domain.WebhookSubscription, error) {
	q := `SELECT ` + webhookSubscriptionColumns + `
		FROM webhook_subscriptions
		WHERE company_id = $1 AND enabled AND events @> ARRAY[$2]::text[]
		ORDER BY created_at`
	return r.querySubscriptions(ctx, q, companyID, event)
}

func (r *WebhookSubscriptionRepo) querySubscriptions(ctx context.Context, q string, args ...any) ([]*domain.WebhookSubscription, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.WebhookSubscription
	for rows.Next() {
		sub, err := scanWebhookSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Update writes what an admin can change: the URL, the events, and whether it
// is on. Re-enabling clears the health — a subscription switched back on after
// the receiver was fixed starts from zero, or nineteen old failures would
// disable it on the next blip.
func (r *WebhookSubscriptionRepo) Update(ctx context.Context, s *domain.WebhookSubscription) error {
	const q = `
		UPDATE webhook_subscriptions
		SET url = $3,
		    events = $4,
		    enabled = $5,
		    consecutive_failures = CASE WHEN $5 AND NOT enabled THEN 0 ELSE consecutive_failures END,
		    disabled_reason = CASE WHEN $5 THEN '' ELSE disabled_reason END,
		    updated_at = NOW()
		WHERE company_id = $1 AND id = $2
		RETURNING ` + webhookSubscriptionColumns
	updated, err := scanWebhookSubscription(
		r.db.QueryRowContext(ctx, q, s.CompanyID, s.ID, s.URL, pq.Array(s.Events), s.Enabled))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	*s = *updated
	return nil
}

func (r *WebhookSubscriptionRepo) Delete(ctx context.Context, companyID, id string) error {
	const q = `DELETE FROM webhook_subscriptions WHERE company_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// RecordSuccess zeroes the counter. Company-less on purpose: the worker holds a
// delivery row, not a session, and the row's subscription id was written by us.
func (r *WebhookSubscriptionRepo) RecordSuccess(ctx context.Context, id string, at time.Time) error {
	const q = `
		UPDATE webhook_subscriptions
		SET consecutive_failures = 0, last_success_at = $2, updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, q, id, at)
	return err
}

// RecordFailure increments and, at the threshold, disables — in one statement,
// so two deliveries failing at once cannot both read nineteen and write twenty.
// `disabled` is true only for the call that made the transition, so exactly one
// caller logs it.
func (r *WebhookSubscriptionRepo) RecordFailure(ctx context.Context, id string, at time.Time, disableAt int) (bool, error) {
	const q = `
		UPDATE webhook_subscriptions
		SET consecutive_failures = consecutive_failures + 1,
		    last_failure_at = $2,
		    enabled = CASE WHEN consecutive_failures + 1 >= $3 THEN FALSE ELSE enabled END,
		    disabled_reason = CASE
		        WHEN consecutive_failures + 1 >= $3 AND enabled
		        THEN 'disabled automatically after ' || ($3)::text || ' consecutive failed deliveries'
		        ELSE disabled_reason END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING consecutive_failures = $3
	`
	// RETURNING reads the row after the update, so "did this call disable it?"
	// cannot be asked as `NOT enabled` — that is true for every failure after
	// the threshold too. The transition is the one where the counter lands
	// exactly on it, and it can only land there once: the next failure makes it
	// larger, and a re-enable resets it to zero.
	var justDisabled bool
	err := r.db.QueryRowContext(ctx, q, id, at, disableAt).Scan(&justDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		// The subscription was deleted between the delivery and its outcome.
		// Nothing to count against, and not an error worth failing a delivery on.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return justDisabled, nil
}
