package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ThreadParticipantRepo persists which agents are in which conversation
// (T-N2).
type ThreadParticipantRepo struct{ db *sql.DB }

func NewThreadParticipantRepo(db *sql.DB) *ThreadParticipantRepo {
	return &ThreadParticipantRepo{db: db}
}

// Add inserts one membership, with the tenant check inside the statement.
//
// The INSERT ... SELECT joins both the thread and the agent to the same
// company, so a participant can never name another tenant's agent or be added
// to another tenant's conversation even if a caller reaches this layer without
// the service's validation — AgentBindingRepo.Create's arrangement, with one
// more table in it because there are two objects to check rather than one.
// Zero rows means one of those checks failed, which is ErrNotFound and not a
// server error.
func (r *ThreadParticipantRepo) Add(ctx context.Context, companyID string, p *domain.ThreadParticipant) error {
	const q = `
		INSERT INTO thread_participants (thread_id, agent_id, added_by)
		SELECT t.id, a.id, $4
		  FROM conversation_threads t
		  JOIN agents a ON a.company_id = t.company_id
		 WHERE t.id = $1 AND a.id = $2 AND t.company_id = $3
		RETURNING id, added_at
	`
	err := r.db.QueryRowContext(ctx, q, p.ThreadID, p.AgentID, companyID, nullOrUUID(p.AddedBy)).
		Scan(&p.ID, &p.AddedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	case err != nil && uniqueViolation(err):
		return domain.ErrAlreadyExists
	case err != nil:
		return fmt.Errorf("insert thread participant: %w", err)
	}
	return nil
}

// ListByThread returns the room, oldest member first, with agent names.
//
// Oldest first rather than newest: the first participant is the one the
// conversation started with, and a list that reorders itself as agents are
// added is a list whose chips move under the cursor.
func (r *ThreadParticipantRepo) ListByThread(ctx context.Context, companyID, threadID string) ([]*domain.ThreadParticipant, error) {
	const q = `
		SELECT p.id, p.thread_id, p.agent_id, a.name,
		       COALESCE(p.added_by::text, ''), p.added_at
		  FROM thread_participants p
		  JOIN agents a ON a.id = p.agent_id
		  JOIN conversation_threads t ON t.id = p.thread_id
		 WHERE p.thread_id = $1 AND t.company_id = $2
		 ORDER BY p.added_at ASC, p.id ASC
	`
	rows, err := r.db.QueryContext(ctx, q, threadID, companyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.ThreadParticipant
	for rows.Next() {
		p := &domain.ThreadParticipant{}
		if err := rows.Scan(&p.ID, &p.ThreadID, &p.AgentID, &p.AgentName,
			&p.AddedBy, &p.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Remove deletes one membership by agent id. ErrNotFound when the agent is not
// in the room, which the service turns into a 404 — a caller removing an agent
// that is not there has made a mistake worth reporting, not a no-op worth
// hiding.
func (r *ThreadParticipantRepo) Remove(ctx context.Context, companyID, threadID, agentID string) error {
	const q = `
		DELETE FROM thread_participants p
		 USING conversation_threads t
		 WHERE p.thread_id = t.id
		   AND p.thread_id = $1 AND p.agent_id = $2 AND t.company_id = $3
	`
	res, err := r.db.ExecContext(ctx, q, threadID, agentID, companyID)
	if err != nil {
		return fmt.Errorf("delete thread participant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete thread participant: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountByThread is what the participant cap is checked against.
func (r *ThreadParticipantRepo) CountByThread(ctx context.Context, companyID, threadID string) (int, error) {
	const q = `
		SELECT COUNT(*)
		  FROM thread_participants p
		  JOIN conversation_threads t ON t.id = p.thread_id
		 WHERE p.thread_id = $1 AND t.company_id = $2
	`
	var n int
	err := r.db.QueryRowContext(ctx, q, threadID, companyID).Scan(&n)
	return n, err
}
