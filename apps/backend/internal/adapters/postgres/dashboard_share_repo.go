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

// DashboardShareRepo persists T-D13's share links.
type DashboardShareRepo struct{ db *sql.DB }

func NewDashboardShareRepo(db *sql.DB) *DashboardShareRepo { return &DashboardShareRepo{db: db} }

const dashboardShareColumns = `
	id, company_id, dashboard_id, token_hash, locked_params, allow_filters,
	coalesce(password_hash, ''), max_refresh_per_hour, coalesce(created_by::text, ''),
	created_at, expires_at, revoked_at, view_count, last_viewed_at`

func scanDashboardShare(sc interface{ Scan(...any) error }) (*domain.DashboardShare, error) {
	var (
		s      domain.DashboardShare
		locked []byte
	)
	if err := sc.Scan(&s.ID, &s.CompanyID, &s.DashboardID, &s.TokenHash, &locked, &s.AllowFilters,
		&s.PasswordHash, &s.MaxRefreshPerHour, &s.CreatedBy,
		&s.CreatedAt, &s.ExpiresAt, &s.RevokedAt, &s.ViewCount, &s.LastViewedAt); err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		if err := json.Unmarshal(locked, &s.LockedParams); err != nil {
			return nil, fmt.Errorf("decode locked_params: %w", err)
		}
	}
	return &s, nil
}

func (r *DashboardShareRepo) Insert(ctx context.Context, s *domain.DashboardShare) error {
	locked := []byte("{}")
	if len(s.LockedParams) > 0 {
		b, err := json.Marshal(s.LockedParams)
		if err != nil {
			return fmt.Errorf("encode locked_params: %w", err)
		}
		locked = b
	}
	const q = `
		INSERT INTO dashboard_shares
			(company_id, dashboard_id, token_hash, locked_params, allow_filters,
			 password_hash, max_refresh_per_hour, created_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,nullif($6,''),$7,nullif($8,'')::uuid,$9)
		RETURNING id, created_at`
	err := r.db.QueryRowContext(ctx, q,
		s.CompanyID, s.DashboardID, s.TokenHash, locked, s.AllowFilters,
		s.PasswordHash, s.MaxRefreshPerHour, s.CreatedBy, s.ExpiresAt,
	).Scan(&s.ID, &s.CreatedAt)
	if err != nil && uniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert dashboard share: %w", err)
	}
	return nil
}

// ByTokenHash is the one query in this repository with no company_id in it, and
// that is the design rather than an omission: the caller is logged out. The
// company comes out of the row and bounds everything read afterwards.
func (r *DashboardShareRepo) ByTokenHash(ctx context.Context, hash string) (*domain.DashboardShare, error) {
	q := `SELECT ` + dashboardShareColumns + ` FROM dashboard_shares WHERE token_hash = $1`
	s, err := scanDashboardShare(r.db.QueryRowContext(ctx, q, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dashboard share by token: %w", err)
	}
	return s, nil
}

func (r *DashboardShareRepo) ListForDashboard(ctx context.Context, companyID, dashboardID string) ([]*domain.DashboardShare, error) {
	q := `SELECT ` + dashboardShareColumns + `
		FROM dashboard_shares WHERE company_id = $1 AND dashboard_id = $2
		ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID, dashboardID)
	if err != nil {
		return nil, fmt.Errorf("list dashboard shares: %w", err)
	}
	defer rows.Close()

	out := []*domain.DashboardShare{}
	for rows.Next() {
		s, err := scanDashboardShare(rows)
		if err != nil {
			return nil, fmt.Errorf("scan dashboard share: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Revoke is idempotent: a second attempt on an already-revoked row succeeds
// without moving the timestamp, so a nervous admin pressing twice does not get
// an error to interpret and the record still says when it was first taken back.
func (r *DashboardShareRepo) Revoke(ctx context.Context, companyID, id string) error {
	const q = `
		UPDATE dashboard_shares SET revoked_at = now()
		WHERE company_id = $1 AND id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, companyID, id)
	if err != nil {
		return fmt.Errorf("revoke dashboard share: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either already revoked or not this company's. Distinguished with one
		// existence check so the caller gets 404 for a stranger's id and 204
		// for a repeat press.
		var exists bool
		if err := r.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM dashboard_shares WHERE company_id = $1 AND id = $2)`,
			companyID, id).Scan(&exists); err != nil {
			return fmt.Errorf("revoke dashboard share: %w", err)
		}
		if !exists {
			return domain.ErrNotFound
		}
	}
	return nil
}

// MarkViewed is best-effort by contract; the caller ignores the error, because
// a dashboard that opened must not fail because its view counter did not.
func (r *DashboardShareRepo) MarkViewed(ctx context.Context, id string) error {
	const q = `UPDATE dashboard_shares SET view_count = view_count + 1, last_viewed_at = $2 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, id, time.Now().UTC())
	return err
}
