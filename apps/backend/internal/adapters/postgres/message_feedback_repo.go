package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

type MessageFeedbackRepo struct{ db *sql.DB }

func NewMessageFeedbackRepo(db *sql.DB) *MessageFeedbackRepo { return &MessageFeedbackRepo{db: db} }

const feedbackColumns = `
	id, company_id, thread_id, message_id, rating, reason,
	actor_kind, actor_ref, created_at, updated_at`

func scanFeedback(s interface{ Scan(...any) error }) (*domain.MessageFeedback, error) {
	f := &domain.MessageFeedback{}
	if err := s.Scan(
		&f.ID, &f.CompanyID, &f.ThreadID, &f.MessageID, &f.Rating, &f.Reason,
		&f.ActorKind, &f.ActorRef, &f.CreatedAt, &f.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return f, nil
}

// Upsert records a verdict, replacing this actor's previous one.
//
// ON CONFLICT rather than a read-then-write: two clicks in quick succession
// from one impatient user are the ordinary case, and a race between them
// should settle on the later click rather than on a unique-violation the UI
// has to explain.
//
// updated_at moves on a replacement and created_at does not, so "when did they
// first say something about this answer" survives a change of mind. That
// distinction is why the row is not simply deleted and re-inserted.
func (r *MessageFeedbackRepo) Upsert(ctx context.Context, f *domain.MessageFeedback) error {
	const q = `
		INSERT INTO message_feedback
			(company_id, thread_id, message_id, rating, reason, actor_kind, actor_ref)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (message_id, actor_kind, actor_ref) DO UPDATE
			SET rating = EXCLUDED.rating,
			    reason = EXCLUDED.reason,
			    updated_at = NOW()
		RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, q,
		f.CompanyID, f.ThreadID, f.MessageID, int16(f.Rating), f.Reason,
		string(f.ActorKind), f.ActorRef,
	).Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt)
}

func (r *MessageFeedbackRepo) GetByMessage(ctx context.Context, companyID, messageID string) ([]*domain.MessageFeedback, error) {
	q := `SELECT ` + feedbackColumns + ` FROM message_feedback
		WHERE company_id = $1 AND message_id = $2
		ORDER BY created_at DESC`
	return r.query(ctx, q, companyID, messageID)
}

// ListByCompany is the "what went wrong lately" list. onlyNegative is the
// filter anyone tuning the agent actually wants — the positives are the
// majority and say nothing actionable.
func (r *MessageFeedbackRepo) ListByCompany(ctx context.Context, companyID string, onlyNegative bool, limit, offset int) ([]*domain.MessageFeedback, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := `WHERE company_id = $1`
	if onlyNegative {
		where += ` AND rating = -1`
	}
	q := `SELECT ` + feedbackColumns + ` FROM message_feedback ` + where +
		` ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	return r.query(ctx, q, companyID, limit, offset)
}

func (r *MessageFeedbackRepo) query(ctx context.Context, q string, args ...any) ([]*domain.MessageFeedback, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MessageFeedback
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Summarize counts the window in one pass. FILTER rather than three queries
// because the numbers must agree with each other: two counts taken a
// millisecond apart can disagree by a vote, and a summary that does not add up
// is one nobody trusts again.
func (r *MessageFeedbackRepo) Summarize(ctx context.Context, companyID string, from, to time.Time) (domain.FeedbackSummary, error) {
	const q = `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE rating = 1),
		       COUNT(*) FILTER (WHERE rating = -1)
		FROM message_feedback
		WHERE company_id = $1 AND created_at >= $2 AND created_at < $3`
	var s domain.FeedbackSummary
	err := r.db.QueryRowContext(ctx, q, companyID, from, to).Scan(&s.Rated, &s.Up, &s.Down)
	return s, err
}

// NegativeMessageIDs answers "which of these did somebody call wrong?" for a
// batch.
//
// A batch because T-Q8 asks it about every cookbook candidate at once, and
// asking per candidate would put a round-trip inside the loop that builds a
// turn's context — on the hot path of a chat turn, before the model is called.
//
// One down-vote is enough to disqualify an example even where others approved.
// The asymmetry is deliberate: an approval can mean "looks plausible" and a
// rejection almost never does.
func (r *MessageFeedbackRepo) NegativeMessageIDs(ctx context.Context, companyID string, messageIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	const q = `
		SELECT DISTINCT message_id::text FROM message_feedback
		WHERE company_id = $1 AND rating = -1 AND message_id = ANY($2)`
	rows, err := r.db.QueryContext(ctx, q, companyID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
