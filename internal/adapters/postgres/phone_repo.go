package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

type PhoneRepo struct{ db *sql.DB }

func NewPhoneRepo(db *sql.DB) *PhoneRepo { return &PhoneRepo{db: db} }

func (r *PhoneRepo) Add(ctx context.Context, p *domain.AllowedPhoneNumber) error {
	const q = `
		INSERT INTO allowed_phone_numbers (company_id, phone_number, label)
		VALUES ($1, $2, $3)
		RETURNING added_at
	`
	err := r.db.QueryRowContext(ctx, q,
		p.CompanyID, normalizePhone(p.PhoneNumber), p.Label,
	).Scan(&p.AddedAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return domain.ErrAlreadyExists
	}
	return err
}

func (r *PhoneRepo) Remove(ctx context.Context, companyID, phoneNumber string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM allowed_phone_numbers WHERE company_id = $1 AND phone_number = $2`,
		companyID, normalizePhone(phoneNumber))
	return err
}

func (r *PhoneRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.AllowedPhoneNumber, error) {
	const q = `SELECT company_id, phone_number, label, added_at FROM allowed_phone_numbers WHERE company_id = $1 ORDER BY added_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.AllowedPhoneNumber
	for rows.Next() {
		p := &domain.AllowedPhoneNumber{}
		if err := rows.Scan(&p.CompanyID, &p.PhoneNumber, &p.Label, &p.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *PhoneRepo) FindCompanyByPhone(ctx context.Context, phoneNumber string) (*domain.AllowedPhoneNumber, error) {
	const q = `SELECT company_id, phone_number, label, added_at FROM allowed_phone_numbers WHERE phone_number = $1`
	p := &domain.AllowedPhoneNumber{}
	err := r.db.QueryRowContext(ctx, q, normalizePhone(phoneNumber)).
		Scan(&p.CompanyID, &p.PhoneNumber, &p.Label, &p.AddedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return p, err
}

// normalizePhone strips whatsapp: prefix and surrounding whitespace so all
// numbers are stored in a canonical E.164 form.
func normalizePhone(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "whatsapp:")
	return p
}
