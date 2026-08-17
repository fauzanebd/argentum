package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SuggestionPickRepo persists next-step chip clicks (058).
type SuggestionPickRepo struct{ db *sql.DB }

func NewSuggestionPickRepo(db *sql.DB) *SuggestionPickRepo { return &SuggestionPickRepo{db: db} }

// Append records one pick.
//
// A plain INSERT with no ON CONFLICT, which is the deliberate difference from
// MessageFeedbackRepo.Upsert: a rating is an opinion that replaces itself and a
// pick is an event that happened. Somebody clicking two of three chips has told
// us both were worth clicking.
func (r *SuggestionPickRepo) Append(ctx context.Context, p *domain.SuggestionPick) error {
	const q = `
		INSERT INTO suggestion_picks (company_id, message_id, idx, recommended, label)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	return r.db.QueryRowContext(ctx, q,
		p.CompanyID, p.MessageID, p.Index, p.Recommended, p.Label,
	).Scan(&p.ID, &p.CreatedAt)
}

// SummaryByCompany counts the answers that offered suggestions and the ones
// that were acted on.
//
// The denominator is a JSONB predicate on `messages` rather than a stored
// counter, because the alternative is a second write on the hot path of every
// answered turn to record a number that is already in the row. The index this
// leans on is the thread/created_at one every transcript read uses; a company
// with a year of history and a taste for this number will want a partial index
// on `metadata ? 'next_steps'`, and that is a migration to write when somebody
// asks for the page rather than now.
func (r *SuggestionPickRepo) SummaryByCompany(ctx context.Context, companyID string, from, to time.Time) (*domain.SuggestionPickSummary, error) {
	if from.IsZero() {
		from = time.Now().AddDate(0, 0, -30)
	}
	if to.IsZero() {
		to = time.Now()
	}
	out := &domain.SuggestionPickSummary{}

	const offeredQ = `
		SELECT COUNT(*)
		  FROM messages m
		  JOIN conversation_threads t ON t.id = m.thread_id
		 WHERE t.company_id = $1
		   AND m.role = 'assistant'
		   AND m.created_at >= $2 AND m.created_at < $3
		   AND m.metadata ? 'next_steps'`
	if err := r.db.QueryRowContext(ctx, offeredQ, companyID, from, to).Scan(&out.Offered); err != nil {
		return nil, err
	}

	// Picked counts DISTINCT messages, not rows. A rate whose numerator counts
	// clicks can exceed 1, which is not a pick rate — it is a different number
	// wearing the same name.
	const pickedQ = `
		SELECT COUNT(*), COUNT(DISTINCT message_id),
		       COUNT(*) FILTER (WHERE recommended)
		  FROM suggestion_picks
		 WHERE company_id = $1 AND created_at >= $2 AND created_at < $3`
	if err := r.db.QueryRowContext(ctx, pickedQ, companyID, from, to).
		Scan(&out.Picks, &out.Picked, &out.RecommendedPicks); err != nil {
		return nil, err
	}
	return out, nil
}
