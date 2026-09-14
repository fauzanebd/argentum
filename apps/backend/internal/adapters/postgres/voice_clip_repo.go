package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// VoiceClipRepo persists voice clips (T-W7, migration 087).
type VoiceClipRepo struct{ db *sql.DB }

// NewVoiceClipRepo wires the control database.
func NewVoiceClipRepo(db *sql.DB) *VoiceClipRepo { return &VoiceClipRepo{db: db} }

// Create writes the clip if its conversation belongs to its company.
//
// The insert selects the conversation rather than taking thread_id on trust.
// The route has already looked it up under the caller's company; this is the
// same question asked again inside the statement that writes, which is
// CapabilityRepo's shape, and no row selected is ErrNotFound. Every value in the
// select list is cast, because a parameter that appears only there has no column
// to infer a type from and would arrive as text.
func (r *VoiceClipRepo) Create(ctx context.Context, c *domain.VoiceClip) error {
	const q = `
		INSERT INTO voice_clips
			(id, company_id, thread_id, user_id, object_key, mime_type, size_bytes,
			 seconds, transcript, language, model, expires_at)
		SELECT $1::uuid, t.company_id, t.id, NULLIF($4, '')::uuid, $5::text, $6::text, $7::bigint,
		       $8::float8, $9::text, $10::text, $11::text, $12::timestamptz
		  FROM conversation_threads t
		 WHERE t.id = $3 AND t.company_id = $2
		RETURNING created_at
	`
	err := r.db.QueryRowContext(ctx, q,
		c.ID, c.CompanyID, c.ThreadID, c.UserID, c.ObjectKey, c.MimeType, c.SizeBytes,
		c.Seconds, c.Transcript, c.Language, c.Model, c.ExpiresAt,
	).Scan(&c.CreatedAt)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows), malformedID(err):
		return domain.ErrNotFound
	default:
		return fmt.Errorf("create voice clip: %w", err)
	}
}

// Due lists what the sweep deletes, across every company.
//
// **The one statement in this file with no company predicate, and it has to
// be.** The sweep is deployment-wide for TypeRetentionPurge's reason: a promise
// that has to be told about each new tenant quietly does not cover them. What
// keeps that from being a cross-tenant read is what it returns — an id, the
// company it belongs to, and a key. No transcript, no user, nothing anybody said
// (TestVoiceClipCrossCompanyReadCarriesNothingSaid).
func (r *VoiceClipRepo) Due(ctx context.Context, now time.Time, limit int) ([]*domain.VoiceClip, error) {
	if limit <= 0 {
		limit = 200
	}
	const q = `
		SELECT id::text, company_id::text, object_key
		  FROM voice_clips
		 WHERE expires_at <= $1 OR thread_id IS NULL
		 ORDER BY expires_at
		 LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, q, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due voice clips: %w", err)
	}
	defer rows.Close()
	var out []*domain.VoiceClip
	for rows.Next() {
		c := &domain.VoiceClip{}
		if err := rows.Scan(&c.ID, &c.CompanyID, &c.ObjectKey); err != nil {
			return nil, fmt.Errorf("scan due voice clip: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due voice clips: %w", err)
	}
	return out, nil
}

// Delete removes one clip's row. A row already gone is not an error: the sweep
// and an erasure can reach the same clip, and "it is gone" is what both wanted.
func (r *VoiceClipRepo) Delete(ctx context.Context, companyID, id string) error {
	const q = `DELETE FROM voice_clips WHERE id = $2 AND company_id = $1`
	if _, err := r.db.ExecContext(ctx, q, companyID, id); err != nil {
		if malformedID(err) {
			return nil
		}
		return fmt.Errorf("delete voice clip: %w", err)
	}
	return nil
}

// DeleteForCompany removes every clip row a company has.
func (r *VoiceClipRepo) DeleteForCompany(ctx context.Context, companyID string) (int, error) {
	const q = `DELETE FROM voice_clips WHERE company_id = $1`
	res, err := r.db.ExecContext(ctx, q, companyID)
	if err != nil {
		if malformedID(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("delete voice clips for company: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted voice clips: %w", err)
	}
	return int(n), nil
}
