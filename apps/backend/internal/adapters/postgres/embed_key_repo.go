package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// EmbedKeyRepo persists the browser-visible credentials (T-19).
type EmbedKeyRepo struct{ db *sql.DB }

func NewEmbedKeyRepo(db *sql.DB) *EmbedKeyRepo { return &EmbedKeyRepo{db: db} }

const embedKeyColumns = `id, company_id, name, client_key, secret_enc,
	allowed_origins, enabled, created_by, last_used_at, revoked_at, created_at`

func (r *EmbedKeyRepo) Create(ctx context.Context, k *domain.EmbedKey) error {
	const q = `
		INSERT INTO embed_keys (company_id, name, client_key, secret_enc, allowed_origins, created_by)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid)
		RETURNING id, enabled, created_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		k.CompanyID, k.Name, k.ClientKey, k.SecretEnc,
		pq.Array(k.AllowedOrigins), k.CreatedBy,
	).Scan(&k.ID, &k.Enabled, &k.CreatedAt); err != nil {
		return fmt.Errorf("insert embed key: %w", err)
	}
	return nil
}

// GetByClientKey is the mint path's read: one indexed lookup on a UNIQUE
// column. Not cached, for APIKeyService.Authenticate's reason — a revoked
// credential has to stop working when the admin says so, and that moment is
// exactly when the key is most likely to be in the wrong hands.
func (r *EmbedKeyRepo) GetByClientKey(ctx context.Context, clientKey string) (*domain.EmbedKey, error) {
	const q = `SELECT ` + embedKeyColumns + ` FROM embed_keys WHERE client_key = $1`
	k, err := scanEmbedKey(r.db.QueryRowContext(ctx, q, clientKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return k, nil
}

func (r *EmbedKeyRepo) GetByID(ctx context.Context, companyID, id string) (*domain.EmbedKey, error) {
	const q = `SELECT ` + embedKeyColumns + ` FROM embed_keys WHERE id = $1 AND company_id = $2`
	k, err := scanEmbedKey(r.db.QueryRowContext(ctx, q, id, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return k, nil
}

// ListByCompany returns every key the company has minted, revoked ones
// included — the list is where a revoke starts, and it is also the answer to
// "which key is that in our page source?".
func (r *EmbedKeyRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.EmbedKey, error) {
	const q = `SELECT ` + embedKeyColumns + `
		FROM embed_keys WHERE company_id = $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.EmbedKey
	for rows.Next() {
		k, err := scanEmbedKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Update writes the two mutable fields. A revoked key is excluded rather than
// updated: re-enabling a credential somebody decided was compromised must not
// be reachable through the edit form.
func (r *EmbedKeyRepo) Update(ctx context.Context, companyID, id string, origins []string, enabled bool) error {
	const q = `UPDATE embed_keys SET allowed_origins = $3, enabled = $4
		WHERE id = $1 AND company_id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id, companyID, pq.Array(origins), enabled)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// Revoke is a tombstone, like api_keys. Audit rows attribute turns to a key id
// and a deleted row turns each of those into an unanswerable question.
func (r *EmbedKeyRepo) Revoke(ctx context.Context, companyID, id string, at time.Time) error {
	const q = `UPDATE embed_keys SET revoked_at = $3, enabled = FALSE
		WHERE id = $1 AND company_id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id, companyID, at)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// TouchLastUsed is throttled by the caller. A failure is never fatal: losing
// the timestamp must not fail the session mint that was recording it.
func (r *EmbedKeyRepo) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE embed_keys SET last_used_at = $2 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, id, at)
	return err
}

func affectedOrNotFound(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanEmbedKey(s rowScanner) (*domain.EmbedKey, error) {
	k := &domain.EmbedKey{}
	var origins pq.StringArray
	var createdBy sql.NullString
	var lastUsed, revoked sql.NullTime
	if err := s.Scan(&k.ID, &k.CompanyID, &k.Name, &k.ClientKey, &k.SecretEnc,
		&origins, &k.Enabled, &createdBy, &lastUsed, &revoked, &k.CreatedAt); err != nil {
		return nil, err
	}
	k.AllowedOrigins = []string(origins)
	k.CreatedBy = createdBy.String
	k.LastUsedAt = nullTimePtr(lastUsed)
	k.RevokedAt = nullTimePtr(revoked)
	k.Status = k.StatusAt()
	return k, nil
}
