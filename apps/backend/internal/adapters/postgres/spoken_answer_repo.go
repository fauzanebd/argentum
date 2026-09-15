package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SpokenAnswerRepo persists spoken answers (T-W8, migration 089).
type SpokenAnswerRepo struct{ db *sql.DB }

// NewSpokenAnswerRepo wires the control database.
func NewSpokenAnswerRepo(db *sql.DB) *SpokenAnswerRepo { return &SpokenAnswerRepo{db: db} }

// ForMessage reads one message's spoken form, scoped by the row's company_id —
// which Save copied from the message's conversation, not from a caller.
func (r *SpokenAnswerRepo) ForMessage(ctx context.Context, companyID, messageID string) (*domain.SpokenAnswer, error) {
	const q = `
		SELECT id::text, company_id::text, COALESCE(message_id::text, ''), object_key, mime_type,
		       size_bytes, spoken_text, refusal, voice, model, chars, created_at, expires_at
		  FROM spoken_answers
		 WHERE company_id = $1 AND message_id = $2
	`
	a := &domain.SpokenAnswer{}
	err := r.db.QueryRowContext(ctx, q, companyID, messageID).Scan(
		&a.ID, &a.CompanyID, &a.MessageID, &a.ObjectKey, &a.MimeType,
		&a.SizeBytes, &a.SpokenText, &a.Refusal, &a.Voice, &a.Model, &a.Chars, &a.CreatedAt, &a.ExpiresAt,
	)
	switch {
	case err == nil:
		return a, nil
	case errors.Is(err, sql.ErrNoRows), malformedID(err):
		return nil, domain.ErrNotFound
	default:
		return nil, fmt.Errorf("read spoken answer: %w", err)
	}
}

// Save writes a message's spoken form if the message belongs to the company,
// replacing an earlier one.
//
// The insert selects the message through its conversation rather than taking
// the company on trust — VoiceClipRepo.Create's shape, one join further, because
// `messages` carries no company_id. No row selected is ErrNotFound.
//
// **Replacing, not refusing a second row.** The one caller that writes twice for
// a message is re-synthesising audio the cache could not read back, and the row
// it replaces describes an object that is not there.
func (r *SpokenAnswerRepo) Save(ctx context.Context, a *domain.SpokenAnswer) error {
	const q = `
		INSERT INTO spoken_answers
			(id, company_id, message_id, object_key, mime_type, size_bytes,
			 spoken_text, refusal, voice, model, chars, expires_at)
		SELECT $1::uuid, t.company_id, m.id, $4::text, $5::text, $6::bigint,
		       $7::text, $8::text, $9::text, $10::text, $11::int, $12::timestamptz
		  FROM messages m
		  JOIN conversation_threads t ON t.id = m.thread_id
		 WHERE m.id = $3 AND t.company_id = $2
		ON CONFLICT (message_id) WHERE message_id IS NOT NULL DO UPDATE SET
			object_key = EXCLUDED.object_key, mime_type = EXCLUDED.mime_type,
			size_bytes = EXCLUDED.size_bytes, spoken_text = EXCLUDED.spoken_text,
			refusal = EXCLUDED.refusal, voice = EXCLUDED.voice, model = EXCLUDED.model,
			chars = EXCLUDED.chars, created_at = now(), expires_at = EXCLUDED.expires_at
		RETURNING id::text, created_at
	`
	err := r.db.QueryRowContext(ctx, q,
		a.ID, a.CompanyID, a.MessageID, a.ObjectKey, a.MimeType, a.SizeBytes,
		a.SpokenText, a.Refusal, a.Voice, a.Model, a.Chars, a.ExpiresAt,
	).Scan(&a.ID, &a.CreatedAt)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows), malformedID(err):
		return domain.ErrNotFound
	default:
		return fmt.Errorf("save spoken answer: %w", err)
	}
}

// Due lists what the sweep deletes, across every company.
//
// VoiceClipRepo.Due's exception, for its reason, and with its limit on what an
// unscoped read may carry: an id, a company and a key. Not the spoken text, not
// the refusal, not the message (TestSpokenAnswerCrossCompanyReadCarriesNoAnswer).
func (r *SpokenAnswerRepo) Due(ctx context.Context, now time.Time, limit int) ([]*domain.SpokenAnswer, error) {
	if limit <= 0 {
		limit = 200
	}
	const q = `
		SELECT id::text, company_id::text, object_key
		  FROM spoken_answers
		 WHERE expires_at <= $1 OR message_id IS NULL
		 ORDER BY expires_at
		 LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, q, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due spoken answers: %w", err)
	}
	defer rows.Close()
	var out []*domain.SpokenAnswer
	for rows.Next() {
		a := &domain.SpokenAnswer{}
		if err := rows.Scan(&a.ID, &a.CompanyID, &a.ObjectKey); err != nil {
			return nil, fmt.Errorf("scan due spoken answer: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due spoken answers: %w", err)
	}
	return out, nil
}

// Delete removes one row. Already gone is not an error, for VoiceClipRepo.Delete's
// reason.
func (r *SpokenAnswerRepo) Delete(ctx context.Context, companyID, id string) error {
	const q = `DELETE FROM spoken_answers WHERE id = $2 AND company_id = $1`
	if _, err := r.db.ExecContext(ctx, q, companyID, id); err != nil {
		if malformedID(err) {
			return nil
		}
		return fmt.Errorf("delete spoken answer: %w", err)
	}
	return nil
}

// DeleteForCompany removes every row a company has.
func (r *SpokenAnswerRepo) DeleteForCompany(ctx context.Context, companyID string) (int, error) {
	const q = `DELETE FROM spoken_answers WHERE company_id = $1`
	res, err := r.db.ExecContext(ctx, q, companyID)
	if err != nil {
		if malformedID(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("delete spoken answers for company: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted spoken answers: %w", err)
	}
	return int(n), nil
}
