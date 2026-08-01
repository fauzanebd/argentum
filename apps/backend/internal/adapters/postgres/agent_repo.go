package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// AgentRepo persists a company's agent roster (T-S1).
type AgentRepo struct{ db *sql.DB }

func NewAgentRepo(db *sql.DB) *AgentRepo { return &AgentRepo{db: db} }

// agentColumns folds the source allowlist into the row rather than issuing a
// second query per agent. ARRAY(...) returns an empty array rather than NULL
// for an agent with no rows in agent_sources, which is the common case — empty
// means unrestricted — and the one a COALESCE around array_agg would have to
// hand-repair.
const agentColumns = `a.id, a.company_id, a.name, a.description, a.persona_prompt,
	a.allowed_tools, a.template_key, a.is_default, a.enabled, a.created_at, a.updated_at,
	ARRAY(SELECT s.connection_id::text FROM agent_sources s
		WHERE s.agent_id = a.id ORDER BY s.connection_id) AS source_ids,
	ARRAY(SELECT m.server_id::text FROM agent_mcp_servers m
		WHERE m.agent_id = a.id ORDER BY m.server_id) AS mcp_server_ids`

// uniqueViolation is Postgres 23505. The roster has two unique indexes an
// ordinary request can collide with — (company_id, lower(name)) and the
// partial one on is_default — and both are the tenant's mistake to fix rather
// than a server fault, so they have to leave this layer as ErrAlreadyExists.
func uniqueViolation(err error) bool {
	var pgErr *pq.Error
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Create writes the agent and its source allowlist as one unit. Without the
// transaction an agent could land scoped to every source when the admin
// scoped it to one, which is the failure direction that matters.
func (r *AgentRepo) Create(ctx context.Context, a *domain.Agent) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const q = `
		INSERT INTO agents (company_id, name, description, persona_prompt, allowed_tools, template_key, is_default, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`
	if err := tx.QueryRowContext(ctx, q,
		a.CompanyID, a.Name, a.Description, a.PersonaPrompt,
		pq.Array(a.AllowedTools), a.TemplateKey, a.IsDefault, a.Enabled,
	).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if uniqueViolation(err) {
			return domain.ErrAlreadyExists
		}
		return fmt.Errorf("insert agent: %w", err)
	}
	if err := replaceSources(ctx, tx, a.CompanyID, a.ID, a.SourceIDs); err != nil {
		return err
	}
	if err := replaceMCPServers(ctx, tx, a.CompanyID, a.ID, a.MCPServerIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *AgentRepo) GetByID(ctx context.Context, companyID, id string) (*domain.Agent, error) {
	q := `SELECT ` + agentColumns + ` FROM agents a WHERE a.id = $1 AND a.company_id = $2`
	a, err := scanAgent(r.db.QueryRowContext(ctx, q, id, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		// Also the answer for another company's agent. The caller turns this
		// into a 404, which is what keeps a uuid guess from confirming that a
		// row exists somewhere else.
		return nil, domain.ErrNotFound
	}
	return a, err
}

// GetDefault returns the company's default agent (T-S2) — the row a turn that
// names no agent runs as. One indexed lookup on the partial unique index that
// makes "exactly one default" true in the first place, rather than a roster
// listing filtered in Go: this runs on the enqueue path of every turn on every
// channel.
func (r *AgentRepo) GetDefault(ctx context.Context, companyID string) (*domain.Agent, error) {
	q := `SELECT ` + agentColumns + ` FROM agents a WHERE a.company_id = $1 AND a.is_default`
	a, err := scanAgent(r.db.QueryRowContext(ctx, q, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		// A company with no roster at all. The caller runs the turn unscoped
		// rather than refusing it — see domain.AgentRepository.
		return nil, domain.ErrNotFound
	}
	return a, err
}

// ListByCompany returns the roster with the default first, then by name. The
// picker T-S3 builds reads in this order, and "which one runs if I do nothing"
// is the first question it has to answer.
func (r *AgentRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.Agent, error) {
	q := `SELECT ` + agentColumns + ` FROM agents a
		WHERE a.company_id = $1 ORDER BY a.is_default DESC, lower(a.name)`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Update rewrites the editable fields and the source allowlist together.
//
// is_default is deliberately not among them: moving the default is SetDefault,
// which clears the previous holder in the same transaction. An UPDATE that set
// is_default here would hit the partial unique index the moment a second agent
// claimed it, and the error would name an index rather than the operation.
//
// template_key is not among them either, for a different reason: it records
// where this agent *came from* (T-B3), which an edit cannot change. Editing
// every field a template prefilled is the supported path — the text is the
// tenant's from the moment they save — and it leaves the provenance intact.
func (r *AgentRepo) Update(ctx context.Context, a *domain.Agent) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const q = `
		UPDATE agents
		SET name = $3, description = $4, persona_prompt = $5, allowed_tools = $6,
			enabled = $7, updated_at = now()
		WHERE id = $1 AND company_id = $2
		RETURNING updated_at
	`
	err = tx.QueryRowContext(ctx, q,
		a.ID, a.CompanyID, a.Name, a.Description, a.PersonaPrompt,
		pq.Array(a.AllowedTools), a.Enabled,
	).Scan(&a.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	case err != nil && uniqueViolation(err):
		return domain.ErrAlreadyExists
	case err != nil:
		return fmt.Errorf("update agent: %w", err)
	}
	if err := replaceSources(ctx, tx, a.CompanyID, a.ID, a.SourceIDs); err != nil {
		return err
	}
	if err := replaceMCPServers(ctx, tx, a.CompanyID, a.ID, a.MCPServerIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *AgentRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM agents WHERE id = $1 AND company_id = $2`, id, companyID)
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

// SetDefault moves the flag. Both statements run in one transaction so the
// partial unique index on (company_id) WHERE is_default never sees two true
// rows — the same shape ConnectionRepo.SetDefault uses, for the same index.
func (r *AgentRepo) SetDefault(ctx context.Context, companyID, agentID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET is_default = false WHERE company_id = $1 AND is_default`, companyID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE agents SET is_default = true, updated_at = now() WHERE id = $1 AND company_id = $2`,
		agentID, companyID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Nothing was promoted, so nothing may be demoted either: rolling back
		// is what stops a bad id from leaving the company with no default at
		// all.
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// replaceSources rewrites one agent's allowlist. The INSERT re-checks
// company_id against db_connections, so an id belonging to another tenant
// inserts nothing rather than granting a cross-company source — the service
// validates the same thing and reports it properly, but this is the layer
// where being wrong would actually hand over data.
func replaceSources(ctx context.Context, tx *sql.Tx, companyID, agentID string, sourceIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_sources WHERE agent_id = $1`, agentID); err != nil {
		return fmt.Errorf("clear agent sources: %w", err)
	}
	if len(sourceIDs) == 0 {
		return nil
	}
	const q = `
		INSERT INTO agent_sources (agent_id, connection_id)
		SELECT $1, c.id FROM db_connections c
		WHERE c.id = ANY($2::uuid[]) AND c.company_id = $3
		ON CONFLICT DO NOTHING
	`
	if _, err := tx.ExecContext(ctx, q, agentID, pq.Array(sourceIDs), companyID); err != nil {
		return fmt.Errorf("set agent sources: %w", err)
	}
	return nil
}

func scanAgent(s rowScanner) (*domain.Agent, error) {
	a := &domain.Agent{}
	var tools, sources, mcpServers pq.StringArray
	if err := s.Scan(
		&a.ID, &a.CompanyID, &a.Name, &a.Description, &a.PersonaPrompt,
		&tools, &a.TemplateKey, &a.IsDefault, &a.Enabled, &a.CreatedAt, &a.UpdatedAt, &sources,
		&mcpServers,
	); err != nil {
		return nil, err
	}
	// []string(nil) and []string{} both marshal to `[]` for the dashboard, but
	// only one of them survives a round trip through a JSON decoder as an
	// empty allowlist rather than a missing field. Normalise here.
	a.AllowedTools = []string(tools)
	if a.AllowedTools == nil {
		a.AllowedTools = []string{}
	}
	a.SourceIDs = []string(sources)
	if a.SourceIDs == nil {
		a.SourceIDs = []string{}
	}
	a.MCPServerIDs = []string(mcpServers)
	if a.MCPServerIDs == nil {
		a.MCPServerIDs = []string{}
	}
	return a, nil
}

// replaceMCPServers rewrites one agent's MCP-server bindings (T-M2/T-M3),
// folded into Create and Update in the same transaction the row and its sources
// are written in — so an edit that changes the binding set and one that changes
// nothing else are the same code path, and a half-applied save cannot leave an
// agent bound to a server the admin removed.
//
// The INSERT re-checks the server's company, so an id belonging to another
// tenant inserts nothing rather than binding an agent to a server it does not
// own. The service validates the same thing for a good error message; this is
// the layer where being wrong would hand over a credentialed egress destination.
func replaceMCPServers(ctx context.Context, tx *sql.Tx, companyID, agentID string, serverIDs []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_mcp_servers WHERE agent_id = $1`, agentID); err != nil {
		return fmt.Errorf("clear agent mcp servers: %w", err)
	}
	if len(serverIDs) == 0 {
		return nil
	}
	const q = `
		INSERT INTO agent_mcp_servers (agent_id, server_id)
		SELECT $1, s.id FROM mcp_servers s
		WHERE s.id = ANY($2::uuid[]) AND s.company_id = $3
		ON CONFLICT DO NOTHING
	`
	if _, err := tx.ExecContext(ctx, q, agentID, pq.Array(serverIDs), companyID); err != nil {
		return fmt.Errorf("set agent mcp servers: %w", err)
	}
	return nil
}
