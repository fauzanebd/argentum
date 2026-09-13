package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
	//
	// The acknowledgement (T-Z8) is written in the same statement as the
	// binding, so a restricted agent's binding never exists for a moment
	// without the acknowledgement it was created with.
	const q = `
		INSERT INTO agent_channel_bindings (company_id, agent_id, channel, external_id, restricted_ack_at, restricted_ack_by)
		SELECT $1, a.id, $3, $4, $5, NULLIF($6, '')::uuid FROM agents a WHERE a.id = $2 AND a.company_id = $1
		RETURNING id, created_at
	`
	var ackAt any
	if b.RestrictedAcknowledgedAt != nil {
		ackAt = *b.RestrictedAcknowledgedAt
	}
	err := r.db.QueryRowContext(ctx, q, b.CompanyID, b.AgentID, string(b.Channel), b.ExternalID,
		ackAt, b.RestrictedAcknowledgedBy).
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
		SELECT ` + bindingColumns + `
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
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// bindingColumns is one binding as Settings reads it, joined to its agent for
// the name and — since T-Z8 — the agent's access mode, so the table can say
// which bindings a restriction has silenced.
const bindingColumns = `b.id, b.company_id, b.agent_id, a.name, b.channel, b.external_id, b.created_at,
	a.access_mode, b.restricted_ack_at, COALESCE(b.restricted_ack_by::text, '')`

func scanBinding(s rowScanner) (*domain.AgentChannelBinding, error) {
	b := &domain.AgentChannelBinding{}
	var mode string
	var ackAt sql.NullTime
	if err := s.Scan(&b.ID, &b.CompanyID, &b.AgentID, &b.AgentName,
		&b.Channel, &b.ExternalID, &b.CreatedAt,
		&mode, &ackAt, &b.RestrictedAcknowledgedBy); err != nil {
		return nil, err
	}
	b.AgentAccessMode = domain.AccessMode(mode)
	b.RestrictedAcknowledgedAt = nullTimePtr(ackAt)
	return b, nil
}

// Acknowledge records, on one binding, that an admin cleared its address to
// reach its agent while the agent is restricted (T-Z8).
//
// An acknowledgement that already exists is kept, time and admin both: the
// first person to say "anyone in #hr may use HR" is the one the record should
// name, and a second press is not a second decision. The UPDATE's SET sees the
// row as it was, which is what lets one statement say "only if nobody has".
func (r *AgentBindingRepo) Acknowledge(
	ctx context.Context, companyID, id, actorID string, at time.Time,
) (*domain.AgentChannelBinding, error) {
	const q = `
		UPDATE agent_channel_bindings b
		   SET restricted_ack_at = COALESCE(b.restricted_ack_at, $3),
		       restricted_ack_by = CASE WHEN b.restricted_ack_at IS NULL
		                                THEN NULLIF($4, '')::uuid
		                                ELSE b.restricted_ack_by END
		  FROM agents a
		 WHERE b.id = $1 AND b.company_id = $2 AND a.id = b.agent_id
		RETURNING ` + bindingColumns
	b, err := scanBinding(r.db.QueryRowContext(ctx, q, id, companyID, at, actorID))
	switch {
	case errors.Is(err, sql.ErrNoRows), malformedID(err):
		// A malformed id is a binding that does not exist, not a 500.
		return nil, domain.ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("acknowledge agent binding: %w", err)
	}
	return b, nil
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
//
// It returns whether the binding carries an acknowledgement (T-Z8), and not the
// agent's access mode. Whether the agent is restricted is internal/authz's to
// decide, and the enqueue path asks it; this read stays one indexed lookup that
// knows nothing about access.
func (r *AgentBindingRepo) AgentForChannel(
	ctx context.Context, companyID string, channel domain.Channel, externalID string,
) (domain.ChannelRoute, error) {
	const q = `
		SELECT b.agent_id, b.restricted_ack_at IS NOT NULL FROM agent_channel_bindings b
		JOIN agents a ON a.id = b.agent_id AND a.enabled
		WHERE b.company_id = $1 AND b.channel = $2 AND b.external_id = $3
	`
	var route domain.ChannelRoute
	err := r.db.QueryRowContext(ctx, q, companyID, string(channel), externalID).
		Scan(&route.AgentID, &route.Acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelRoute{}, domain.ErrNotFound
	}
	return route, err
}
