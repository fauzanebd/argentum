package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// AgentBindingRepo persists which agent answers on which channel address
// (T-S4).
type AgentBindingRepo struct{ db *sql.DB }

func NewAgentBindingRepo(db *sql.DB) *AgentBindingRepo { return &AgentBindingRepo{db: db} }

func (r *AgentBindingRepo) Create(ctx context.Context, b *domain.AgentChannelBinding) error {
	// The INSERT ... SELECT re-checks the agent against the same company, so a
	// binding can never name another tenant's agent even if a caller reaches
	// this layer without the service's validation. Zero rows means that check
	// failed, which is ErrNotFound rather than a server error.
	const q = `
		INSERT INTO agent_channel_bindings (company_id, agent_id, channel, external_id)
		SELECT $1, a.id, $3, $4 FROM agents a WHERE a.id = $2 AND a.company_id = $1
		RETURNING id, created_at
	`
	err := r.db.QueryRowContext(ctx, q, b.CompanyID, b.AgentID, string(b.Channel), b.ExternalID).
		Scan(&b.ID, &b.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	case err != nil && uniqueViolation(err):
		return domain.ErrAlreadyExists
	case err != nil:
		return fmt.Errorf("insert agent binding: %w", err)
	}
	return nil
}

// ListByCompany returns every binding with its agent's name, ordered so the
// table reads by channel and then by address.
func (r *AgentBindingRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.AgentChannelBinding, error) {
	const q = `
		SELECT b.id, b.company_id, b.agent_id, a.name, b.channel, b.external_id, b.created_at
		FROM agent_channel_bindings b
		JOIN agents a ON a.id = b.agent_id
		WHERE b.company_id = $1
		ORDER BY b.channel, b.external_id
	`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.AgentChannelBinding
	for rows.Next() {
		b := &domain.AgentChannelBinding{}
		if err := rows.Scan(&b.ID, &b.CompanyID, &b.AgentID, &b.AgentName,
			&b.Channel, &b.ExternalID, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *AgentBindingRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM agent_channel_bindings WHERE id = $1 AND company_id = $2`, id, companyID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AgentForChannel resolves an inbound address to its agent. It runs on the
// enqueue path of every WhatsApp, Discord and Lark message, so it is one
// indexed lookup on idx_agent_binding_channel_ref and nothing else.
//
// The join requires `a.enabled`: an admin who disables an agent has taken it
// out of service, and a channel still pointed at it would stop answering with
// no visible cause. Falling back to the default is the same answer an unbound
// channel gets, which is also the answer this returns for a binding the FK
// cascade already removed.
func (r *AgentBindingRepo) AgentForChannel(
	ctx context.Context, companyID string, channel domain.Channel, externalID string,
) (string, error) {
	const q = `
		SELECT b.agent_id FROM agent_channel_bindings b
		JOIN agents a ON a.id = b.agent_id AND a.enabled
		WHERE b.company_id = $1 AND b.channel = $2 AND b.external_id = $3
	`
	var agentID string
	err := r.db.QueryRowContext(ctx, q, companyID, string(channel), externalID).Scan(&agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return agentID, err
}
