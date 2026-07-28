package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// WebhookDeliveryRepo persists the outbound callback log (T-A2).
type WebhookDeliveryRepo struct{ db *sql.DB }

func NewWebhookDeliveryRepo(db *sql.DB) *WebhookDeliveryRepo {
	return &WebhookDeliveryRepo{db: db}
}

const webhookDeliveryColumns = `
	id, company_id, event, url, payload, status, attempts, last_status, last_error,
	created_at, delivered_at`

func scanWebhookDelivery(s interface{ Scan(...any) error }) (*domain.WebhookDelivery, error) {
	d := &domain.WebhookDelivery{}
	var status string
	var deliveredAt sql.NullTime
	if err := s.Scan(
		&d.ID, &d.CompanyID, &d.Event, &d.URL, &d.Payload, &status,
		&d.Attempts, &d.LastStatus, &d.LastError, &d.CreatedAt, &deliveredAt,
	); err != nil {
		return nil, err
	}
	d.Status = domain.WebhookDeliveryStatus(status)
	if deliveredAt.Valid {
		t := deliveredAt.Time
		d.DeliveredAt = &t
	}
	return d, nil
}

func (r *WebhookDeliveryRepo) Create(ctx context.Context, d *domain.WebhookDelivery) error {
	const q = `
		INSERT INTO webhook_deliveries (id, company_id, event, url, payload, status)
		VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`
	if d.Status == "" {
		d.Status = domain.WebhookPending
	}
	return r.db.QueryRowContext(ctx, q,
		d.ID, d.CompanyID, d.Event, d.URL, d.Payload, string(d.Status),
	).Scan(&d.ID, &d.CreatedAt)
}

func (r *WebhookDeliveryRepo) Get(ctx context.Context, id string) (*domain.WebhookDelivery, error) {
	q := `SELECT ` + webhookDeliveryColumns + ` FROM webhook_deliveries WHERE id = $1`
	d, err := scanWebhookDelivery(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// RecordAttempt appends one attempt's outcome.
//
// attempts is incremented rather than assigned: the sender is asynq-driven and
// a retry does not know which attempt it is. delivered_at is set only on the
// delivered transition, so a row that succeeded on the fourth try still says
// when it actually landed.
func (r *WebhookDeliveryRepo) RecordAttempt(
	ctx context.Context, id string, status domain.WebhookDeliveryStatus,
	httpStatus int, errMsg string, at time.Time,
) error {
	const q = `
		UPDATE webhook_deliveries
		SET attempts = attempts + 1,
		    status = $2,
		    last_status = $3,
		    last_error = $4,
		    delivered_at = CASE WHEN $2 = 'delivered' THEN $5 ELSE delivered_at END
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, q, id, string(status), httpStatus, errMsg, at.UTC())
	return err
}

func (r *WebhookDeliveryRepo) ListByCompany(ctx context.Context, companyID string, limit int) ([]*domain.WebhookDelivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + webhookDeliveryColumns + ` FROM webhook_deliveries
		WHERE company_id = $1 ORDER BY created_at DESC LIMIT $2`
	rows, err := r.db.QueryContext(ctx, q, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.WebhookDelivery
	for rows.Next() {
		d, err := scanWebhookDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
