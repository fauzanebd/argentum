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

// APIKeyRepo persists company-scoped machine credentials (T-13).
type APIKeyRepo struct{ db *sql.DB }

func NewAPIKeyRepo(db *sql.DB) *APIKeyRepo { return &APIKeyRepo{db: db} }

const apiKeyColumns = `id, company_id, name, key_prefix, key_hash, scopes,
	created_by, last_used_at, expires_at, revoked_at, created_at`

func (r *APIKeyRepo) Create(ctx context.Context, k *domain.APIKey) error {
	const q = `
		INSERT INTO api_keys (company_id, name, key_prefix, key_hash, scopes, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7)
		RETURNING id, created_at
	`
	var expires any
	if k.ExpiresAt != nil {
		expires = *k.ExpiresAt
	}
	if err := r.db.QueryRowContext(ctx, q,
		k.CompanyID, k.Name, k.KeyPrefix, k.KeyHash,
		pq.Array(k.SortedScopeStrings()), k.CreatedBy, expires,
	).Scan(&k.ID, &k.CreatedAt); err != nil {
		return fmt.Errorf("insert api key: %w", err)
	}
	return nil
}

// GetByPrefix is the authentication read: one indexed lookup on a UNIQUE
// column, on every request. It is deliberately not cached — see
// app.APIKeyService.Authenticate for why "revoked means revoked now" is worth
// a query per call.
func (r *APIKeyRepo) GetByPrefix(ctx context.Context, prefix string) (*domain.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_prefix = $1`
	k, err := scanAPIKey(r.db.QueryRowContext(ctx, q, prefix))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return k, nil
}

// ListByCompany returns every key the company has ever minted, revoked ones
// included. A revoked key is the answer to "what was that credential in our
// CI config?", so hiding it would remove the only record an admin can act on.
func (r *APIKeyRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + `
		FROM api_keys WHERE company_id = $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke is idempotent in effect but not in reporting: a second revoke of the
// same key affects zero rows and returns ErrNotFound, so the dashboard cannot
// show "revoked" for a key some other admin already revoked at a different
// time. The first revocation timestamp is the one that stands.
func (r *APIKeyRepo) Revoke(ctx context.Context, companyID, id string, at time.Time) error {
	const q = `UPDATE api_keys SET revoked_at = $3
		WHERE id = $1 AND company_id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id, companyID, at)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// TouchLastUsed is throttled by the caller, not here. A failure is not
// returned as fatal anywhere upstream: losing a "last used" timestamp must
// never fail the request it was recording.
func (r *APIKeyRepo) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, id, at)
	return err
}

func scanAPIKey(s rowScanner) (*domain.APIKey, error) {
	k := &domain.APIKey{}
	var scopes pq.StringArray
	var createdBy sql.NullString
	var lastUsed, expires, revoked sql.NullTime
	if err := s.Scan(&k.ID, &k.CompanyID, &k.Name, &k.KeyPrefix, &k.KeyHash,
		&scopes, &createdBy, &lastUsed, &expires, &revoked, &k.CreatedAt); err != nil {
		return nil, err
	}
	k.Scopes = make([]domain.Scope, 0, len(scopes))
	for _, s := range scopes {
		k.Scopes = append(k.Scopes, domain.Scope(s))
	}
	k.CreatedBy = createdBy.String
	k.LastUsedAt = nullTimePtr(lastUsed)
	k.ExpiresAt = nullTimePtr(expires)
	k.RevokedAt = nullTimePtr(revoked)
	k.Status = k.StatusAt(time.Now())
	return k, nil
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
