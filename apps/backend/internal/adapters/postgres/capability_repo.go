package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// CapabilityRepo persists capability grants (T-Z1, migration 083).
//
// Every statement starts from the users row, scoped by company, rather than
// from user_capabilities alone. That is what makes a user id from another
// company a not-found on all three methods: a bare INSERT would write a grant
// naming another company's person under this company's id, and a bare SELECT
// would answer "holds nothing" for somebody who is not here at all.
type CapabilityRepo struct{ db *sql.DB }

// NewCapabilityRepo wires the control database.
func NewCapabilityRepo(db *sql.DB) *CapabilityRepo { return &CapabilityRepo{db: db} }

// ListForUser returns what userID holds, ordered by capability name.
//
// One statement, and the LEFT JOIN is what makes it one: a user who holds
// nothing yields a single row of NULLs, and a user who is not in the company
// yields no row at all — which is the difference between an empty list and a
// 404, answered without a second round trip.
func (r *CapabilityRepo) ListForUser(ctx context.Context, companyID, userID string) ([]domain.CapabilityGrant, error) {
	const q = `
		SELECT c.capability, c.granted_by::text, c.granted_at
		  FROM users u
		  LEFT JOIN user_capabilities c
		         ON c.company_id = u.company_id AND c.user_id = u.id
		 WHERE u.id = $2 AND u.company_id = $1
		 ORDER BY c.capability
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, userID)
	if err != nil {
		if malformedID(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("list capabilities: %w", err)
	}
	defer rows.Close()

	found := false
	out := []domain.CapabilityGrant{}
	for rows.Next() {
		found = true
		var (
			capability sql.NullString
			grantedBy  sql.NullString
			grantedAt  sql.NullTime
		)
		if err := rows.Scan(&capability, &grantedBy, &grantedAt); err != nil {
			return nil, fmt.Errorf("scan capability: %w", err)
		}
		if !capability.Valid {
			continue // the LEFT JOIN's row for a user who holds nothing
		}
		out = append(out, domain.CapabilityGrant{
			UserID:     userID,
			Capability: domain.Capability(capability.String),
			GrantedBy:  grantedBy.String,
			GrantedAt:  grantedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		if malformedID(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("list capabilities: %w", err)
	}
	if !found {
		return nil, domain.ErrNotFound
	}
	return out, nil
}

// Grant writes the grant if userID is a user of companyID, and is idempotent.
//
// A data-modifying CTE runs whether or not the outer query reads it, so the
// count of `target` answers "is this a user of the company" in the same
// statement that writes — no window between the check and the insert. ON
// CONFLICT DO NOTHING is the idempotence: the second grant touches no row, and
// the first row's granted_by stands.
func (r *CapabilityRepo) Grant(ctx context.Context, companyID, userID string, c domain.Capability, grantedBy string) error {
	const q = `
		WITH target AS (
			SELECT id FROM users WHERE id = $2 AND company_id = $1
		), granted AS (
			INSERT INTO user_capabilities (company_id, user_id, capability, granted_by)
			SELECT $1, id, $3, NULLIF($4, '')::uuid FROM target
			ON CONFLICT (company_id, user_id, capability) DO NOTHING
		)
		SELECT count(*) FROM target
	`
	return countTarget(ctx, r.db, "grant capability", q, companyID, userID, string(c), grantedBy)
}

// Revoke deletes the grant if it exists, and is idempotent. Same statement
// shape as Grant and for the same reason: a user of another company is a
// not-found, not a successful delete of nothing.
func (r *CapabilityRepo) Revoke(ctx context.Context, companyID, userID string, c domain.Capability) error {
	const q = `
		WITH target AS (
			SELECT id FROM users WHERE id = $2 AND company_id = $1
		), revoked AS (
			DELETE FROM user_capabilities
			 WHERE company_id = $1 AND user_id = $2 AND capability = $3
		)
		SELECT count(*) FROM target
	`
	return countTarget(ctx, r.db, "revoke capability", q, companyID, userID, string(c))
}

// countTarget runs a grant or revoke statement whose outer query counts its
// company-scoped `target` CTE, and maps a count of zero — no such user, or no
// such resource, in this company — to ErrNotFound. Shared by the capability and
// resource-grant repositories, whose statements have the same shape for the
// same reason.
func countTarget(ctx context.Context, db *sql.DB, op, q string, args ...any) error {
	var n int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		if malformedID(err) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("%s: %w", op, err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// malformedID reports Postgres refusing a string as a uuid (SQLSTATE 22P02,
// invalid_text_representation). The user id arrives in a URL, so it is
// caller-supplied, and "that is not a uuid" and "no such user" are the same
// answer to the caller: there is nobody by that id here. Left unmapped it is a
// 500 carrying the driver's message, which is both wrong and chatty.
func malformedID(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "22P02"
}
