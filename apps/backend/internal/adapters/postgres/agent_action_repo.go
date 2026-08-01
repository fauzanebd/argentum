package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// AgentActionRepo is the append-only store behind the agent audit log (T-05).
type AgentActionRepo struct{ db *sql.DB }

func NewAgentActionRepo(db *sql.DB) *AgentActionRepo { return &AgentActionRepo{db: db} }

const agentActionColumns = `id, company_id, thread_id, message_id, actor_kind, actor_ref, channel,
	COALESCE(agent_id::text, ''), tool_name, source_id, COALESCE(mcp_server_id::text, ''),
	args_redacted, args_hash,
	result_status, error_text, rows_returned, duration_ms, request_id, created_at`

// Create writes one action. NULLIF(...)::uuid is what lets the caller pass an
// empty thread or message id: the columns hold real UUIDs but a tool call made
// outside a chat turn (the API's schema-cache refresh, the eval harness) has
// neither, and an audit row without a thread is still worth having.
func (r *AgentActionRepo) Create(ctx context.Context, a *domain.AgentAction) error {
	const q = `
		INSERT INTO agent_actions (
			company_id, thread_id, message_id, actor_kind, actor_ref, channel,
			agent_id, tool_name, source_id, mcp_server_id, args_redacted, args_hash, result_status,
			error_text, rows_returned, duration_ms, request_id
		) VALUES (
			$1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6,
			NULLIF($7, '')::uuid, $8, $9, NULLIF($10, '')::uuid, $11::jsonb, $12, $13,
			NULLIF($14, ''), $15, $16, $17
		)
		RETURNING id, created_at
	`
	args := a.ArgsRedacted
	if len(args) == 0 {
		args = []byte("{}")
	}
	var rows interface{}
	if a.RowsReturned != nil {
		rows = *a.RowsReturned
	}
	if err := r.db.QueryRowContext(ctx, q,
		a.CompanyID, a.ThreadID, a.MessageID, string(a.ActorKind), a.ActorRef, string(a.Channel),
		a.AgentID, a.ToolName, a.SourceID, a.MCPServerID, string(args), a.ArgsHash, string(a.ResultStatus),
		a.ErrorText, rows, a.DurationMS, a.RequestID,
	).Scan(&a.ID, &a.CreatedAt); err != nil {
		return fmt.Errorf("insert agent action: %w", err)
	}
	return nil
}

// ListByCompany returns the company's actions newest first. company_id is the
// first predicate on every read: the audit log is the one table where a
// cross-tenant leak would hand over another company's SQL verbatim.
func (r *AgentActionRepo) ListByCompany(
	ctx context.Context, companyID string, f domain.AgentActionFilter,
) ([]*domain.AgentAction, error) {
	where := []string{"company_id = $1", "created_at >= $2", "created_at < $3"}
	args := []interface{}{companyID, f.From, f.To}
	if f.ThreadID != "" {
		args = append(args, f.ThreadID)
		where = append(where, fmt.Sprintf("thread_id = $%d::uuid", len(args)))
	}
	if f.Tool != "" {
		args = append(args, f.Tool)
		where = append(where, fmt.Sprintf("tool_name = $%d", len(args)))
	}
	if f.RequestID != "" {
		args = append(args, f.RequestID)
		where = append(where, fmt.Sprintf("request_id = $%d", len(args)))
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)

	q := `SELECT ` + agentActionColumns + ` FROM agent_actions WHERE ` +
		strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent actions: %w", err)
	}
	defer rows.Close()

	var out []*domain.AgentAction
	for rows.Next() {
		a, err := scanAgentAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAgentAction(s rowScanner) (*domain.AgentAction, error) {
	a := &domain.AgentAction{}
	var threadID, messageID, errText sql.NullString
	var actorKind, channel, status string
	var rowsReturned sql.NullInt64
	if err := s.Scan(
		&a.ID, &a.CompanyID, &threadID, &messageID, &actorKind, &a.ActorRef, &channel,
		&a.AgentID, &a.ToolName, &a.SourceID, &a.MCPServerID, &a.ArgsRedacted, &a.ArgsHash, &status, &errText,
		&rowsReturned, &a.DurationMS, &a.RequestID, &a.CreatedAt,
	); err != nil {
		return nil, err
	}
	a.ThreadID = threadID.String
	a.MessageID = messageID.String
	a.ActorKind = domain.ActorKind(actorKind)
	a.Channel = domain.Channel(channel)
	a.ResultStatus = domain.ActionStatus(status)
	a.ErrorText = errText.String
	if rowsReturned.Valid {
		n := int(rowsReturned.Int64)
		a.RowsReturned = &n
	}
	return a, nil
}
