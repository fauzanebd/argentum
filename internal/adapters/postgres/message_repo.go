package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

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
	const q = `
		SELECT id, thread_id, role, content,
			COALESCE(tool_calls::text, ''),
			tokens_in, tokens_out, latency_ms,
			COALESCE(metadata::text, ''),
			created_at
		FROM messages
		WHERE thread_id = $1
		ORDER BY created_at ASC LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, q, threadID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Message
	for rows.Next() {
		m := &domain.Message{}
		var role, tc, md string
		if err := rows.Scan(&m.ID, &m.ThreadID, &role, &m.Content, &tc,
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
		out = append(out, m)
	}
	return out, rows.Err()
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
