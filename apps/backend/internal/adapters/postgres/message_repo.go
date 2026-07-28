package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type MessageRepo struct{ db *sql.DB }

func NewMessageRepo(db *sql.DB) *MessageRepo { return &MessageRepo{db: db} }

func (r *MessageRepo) Append(ctx context.Context, m *domain.Message) error {
	const q = `
		INSERT INTO messages (thread_id, role, content, tool_calls, tokens_in, tokens_out, latency_ms, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at
	`
	tc, _ := json.Marshal(m.ToolCalls)
	md, _ := json.Marshal(m.Metadata)
	return r.db.QueryRowContext(ctx, q,
		m.ThreadID, string(m.Role), m.Content, jsonbOrNull(tc),
		m.TokensIn, m.TokensOut, m.LatencyMs, jsonbOrNull(md),
	).Scan(&m.ID, &m.CreatedAt)
}

func (r *MessageRepo) ListByThread(ctx context.Context, threadID string, limit, offset int) ([]*domain.Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + messageColumns + ` FROM messages
		WHERE thread_id = $1
		ORDER BY created_at ASC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, q, threadID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// messageColumns is the SELECT list the keyset reads share.
const messageColumns = `
	id, thread_id, role, content,
	COALESCE(tool_calls::text, ''),
	tokens_in, tokens_out, latency_ms,
	COALESCE(metadata::text, ''),
	created_at`

const (
	maxMessagePage     = 200
	defaultMessagePage = 50
)

func scanMessage(s interface{ Scan(...any) error }) (*domain.Message, error) {
	m := &domain.Message{}
	var role, tc, md string
	if err := s.Scan(&m.ID, &m.ThreadID, &role, &m.Content, &tc,
		&m.TokensIn, &m.TokensOut, &m.LatencyMs, &md, &m.CreatedAt); err != nil {
		return nil, err
	}
	m.Role = domain.MessageRole(role)
	if tc != "" {
		_ = json.Unmarshal([]byte(tc), &m.ToolCalls)
	}
	if md != "" {
		_ = json.Unmarshal([]byte(md), &m.Metadata)
	}
	return m, nil
}

// ListPageByThread walks a transcript oldest-first, one keyset page at a time
// (T-A3).
//
// The predicate is `>` where every other listing in this package uses `<`,
// because this is the one collection whose natural reading order is the order
// it was written in. It still compares the (created_at, id) pair rather than
// the timestamp alone: two messages of one turn can share a microsecond, and
// comparing only the timestamp drops every row that ties with the last one on
// the previous page.
func (r *MessageRepo) ListPageByThread(ctx context.Context, threadID string, f domain.MessageFilter) ([]*domain.Message, bool, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultMessagePage
	}
	if limit > maxMessagePage {
		limit = maxMessagePage
	}

	args := []any{threadID}
	where := ` WHERE thread_id = $1`
	if f.CursorID != "" && !f.CursorTime.IsZero() {
		args = append(args, f.CursorTime, f.CursorID)
		where += ` AND (created_at, id) > ($` + itoa(len(args)-1) + `, $` + itoa(len(args)) + `::uuid)`
	}
	args = append(args, limit+1)
	q := `SELECT ` + messageColumns + ` FROM messages` + where +
		` ORDER BY created_at ASC, id ASC LIMIT $` + itoa(len(args))

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]*domain.Message, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// LatestByThread returns the newest message of any role.
func (r *MessageRepo) LatestByThread(ctx context.Context, threadID string) (*domain.Message, error) {
	q := `SELECT ` + messageColumns + ` FROM messages
		WHERE thread_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`
	m, err := scanMessage(r.db.QueryRowContext(ctx, q, threadID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// LatestAssistantSince is "has this turn answered yet?" as one query.
//
// It is what lets an SSE stream that attached after the worker had already
// published `final` still deliver the answer, instead of holding a connection
// open waiting for an event that was published into an empty room. Redis
// pub/sub keeps nothing for a subscriber that was not there, so the persisted
// message log is the only durable record of a turn's result.
func (r *MessageRepo) LatestAssistantSince(ctx context.Context, threadID string, since time.Time) (*domain.Message, error) {
	q := `SELECT ` + messageColumns + ` FROM messages
		WHERE thread_id = $1 AND role = 'assistant' AND created_at >= $2
		ORDER BY created_at DESC, id DESC LIMIT 1`
	m, err := scanMessage(r.db.QueryRowContext(ctx, q, threadID, since))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (r *MessageRepo) CountByThread(ctx context.Context, threadID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE thread_id = $1`, threadID).Scan(&n)
	return n, err
}

// jsonbOrNull returns nil so the Postgres jsonb column stores NULL when no
// payload was provided, instead of the literal string "null".
func jsonbOrNull(b []byte) interface{} {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
