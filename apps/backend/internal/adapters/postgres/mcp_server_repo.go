package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// MCPServerRepo stores the tenant's MCP servers and the tools discovery found
// on them (T-M1).
//
// Every method takes the company id beside the row id, unlike ConnectionRepo's
// GetByID. The ids here are bare uuids in an admin-only URL, and a repository
// that will answer for any company is one forgotten check away from a
// cross-tenant read.
type MCPServerRepo struct{ db *sql.DB }

func NewMCPServerRepo(db *sql.DB) *MCPServerRepo { return &MCPServerRepo{db: db} }

const mcpServerColumns = `id, company_id, name, description, url, transport,
		auth_encrypted, enabled, last_probed_at, probe_error, created_at, updated_at`

func scanMCPServer(row interface {
	Scan(dest ...interface{}) error
}) (*domain.MCPServer, error) {
	s := &domain.MCPServer{}
	var probedAt sql.NullTime
	var auth []byte
	if err := row.Scan(
		&s.ID, &s.CompanyID, &s.Name, &s.Description, &s.URL, &s.Transport,
		&auth, &s.Enabled, &probedAt, &s.ProbeError, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return nil, err
	}
	s.AuthEncrypted = auth
	// Derived on read rather than stored: the column is the fact, and a second
	// boolean that could disagree with it is a bug waiting for a partial write.
	s.HasAuth = len(auth) > 0
	if probedAt.Valid {
		t := probedAt.Time
		s.LastProbedAt = &t
	}
	return s, nil
}

func (r *MCPServerRepo) Create(ctx context.Context, s *domain.MCPServer) error {
	const q = `
		INSERT INTO mcp_servers (company_id, name, description, url, transport, auth_encrypted, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, q,
		s.CompanyID, s.Name, s.Description, s.URL, string(s.Transport), s.AuthEncrypted, s.Enabled,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil && uniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert mcp server: %w", err)
	}
	s.HasAuth = len(s.AuthEncrypted) > 0
	return nil
}

func (r *MCPServerRepo) GetByID(ctx context.Context, companyID, id string) (*domain.MCPServer, error) {
	q := `SELECT ` + mcpServerColumns + ` FROM mcp_servers WHERE company_id = $1 AND id = $2`
	s, err := scanMCPServer(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return s, err
}

func (r *MCPServerRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.MCPServer, error) {
	q := `SELECT ` + mcpServerColumns + ` FROM mcp_servers WHERE company_id = $1 ORDER BY lower(name)`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer rows.Close()

	out := []*domain.MCPServer{}
	for rows.Next() {
		s, err := scanMCPServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mcp server: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Update rewrites what an admin may edit. auth_encrypted is written only when
// the caller supplies one: an edit that omits the token keeps the stored one,
// because the form cannot show it back and a blank field means "unchanged"
// rather than "delete it". Clearing is ClearAuth's job, and it is explicit.
func (r *MCPServerRepo) Update(ctx context.Context, s *domain.MCPServer) error {
	const withAuth = `
		UPDATE mcp_servers
		   SET name = $3, description = $4, url = $5, transport = $6,
		       auth_encrypted = $7, enabled = $8, updated_at = now()
		 WHERE company_id = $1 AND id = $2
		RETURNING updated_at`
	const keepAuth = `
		UPDATE mcp_servers
		   SET name = $3, description = $4, url = $5, transport = $6,
		       enabled = $7, updated_at = now()
		 WHERE company_id = $1 AND id = $2
		RETURNING updated_at`

	var err error
	if s.AuthEncrypted != nil {
		err = r.db.QueryRowContext(ctx, withAuth,
			s.CompanyID, s.ID, s.Name, s.Description, s.URL, string(s.Transport),
			s.AuthEncrypted, s.Enabled,
		).Scan(&s.UpdatedAt)
	} else {
		err = r.db.QueryRowContext(ctx, keepAuth,
			s.CompanyID, s.ID, s.Name, s.Description, s.URL, string(s.Transport), s.Enabled,
		).Scan(&s.UpdatedAt)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	case err != nil && uniqueViolation(err):
		return domain.ErrAlreadyExists
	case err != nil:
		return fmt.Errorf("update mcp server: %w", err)
	}
	return nil
}

// ClearAuth removes a stored token. Separate from Update for the reason above:
// "the admin left the token field empty" and "the admin wants no token" are
// different intentions, and only one of them should be able to break a working
// server.
func (r *MCPServerRepo) ClearAuth(ctx context.Context, companyID, id string) error {
	const q = `UPDATE mcp_servers SET auth_encrypted = NULL, updated_at = now()
	            WHERE company_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, companyID, id)
	if err != nil {
		return fmt.Errorf("clear mcp server auth: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *MCPServerRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM mcp_servers WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete mcp server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// RecordProbe stores the outcome of a discovery attempt and touches nothing
// else. A failed probe is a saved row with a reason on it.
func (r *MCPServerRepo) RecordProbe(ctx context.Context, companyID, id string, at time.Time, probeErr string) error {
	const q = `UPDATE mcp_servers SET last_probed_at = $3, probe_error = $4
	            WHERE company_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, companyID, id, at, probeErr)
	if err != nil {
		return fmt.Errorf("record mcp probe: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const mcpToolColumns = `id, server_id, tool_name, description, input_schema,
		read_only, approved, approved_digest, discovered_at`

func (r *MCPServerRepo) ListTools(ctx context.Context, serverID string) ([]*domain.MCPServerTool, error) {
	q := `SELECT ` + mcpToolColumns + ` FROM mcp_server_tools WHERE server_id = $1 ORDER BY tool_name`
	rows, err := r.db.QueryContext(ctx, q, serverID)
	if err != nil {
		return nil, fmt.Errorf("list mcp tools: %w", err)
	}
	defer rows.Close()

	out := []*domain.MCPServerTool{}
	for rows.Next() {
		t := &domain.MCPServerTool{}
		var schema []byte
		if err := rows.Scan(&t.ID, &t.ServerID, &t.ToolName, &t.Description, &schema,
			&t.ReadOnly, &t.Approved, &t.ApprovedDigest, &t.DiscoveredAt); err != nil {
			return nil, fmt.Errorf("scan mcp tool: %w", err)
		}
		t.InputSchema = schema
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReplaceTools applies one discovery, in one transaction.
//
// An upsert per tool plus a delete of the rest, rather than a truncate and
// reinsert, because the review state is the thing worth keeping: a refresh must
// not silently unapprove every tool an admin already read. What it does not
// keep is the approval of a tool whose text changed — approved stays true and
// approved_digest stays as it was, which is exactly what makes
// MCPServerTool.Drifted able to say so.
func (r *MCPServerRepo) ReplaceTools(ctx context.Context, serverID string, tools []*domain.MCPServerTool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.ToolName)
		const q = `
			INSERT INTO mcp_server_tools (server_id, tool_name, description, input_schema, discovered_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (server_id, tool_name) DO UPDATE
				SET description = EXCLUDED.description,
				    input_schema = EXCLUDED.input_schema,
				    discovered_at = now()`
		if _, err := tx.ExecContext(ctx, q, serverID, t.ToolName, t.Description, []byte(t.InputSchema)); err != nil {
			return fmt.Errorf("upsert mcp tool %q: %w", t.ToolName, err)
		}
	}

	// A tool the server no longer offers goes, approval and all. Keeping it
	// would leave an approved row for something nothing can call, which reads
	// on the review screen as a capability the tenant has.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM mcp_server_tools WHERE server_id = $1 AND NOT (tool_name = ANY($2))`,
		serverID, pq.Array(names),
	); err != nil {
		return fmt.Errorf("prune mcp tools: %w", err)
	}
	return tx.Commit()
}

// SetToolReview records one admin decision. The digest is passed in rather than
// computed here, because what was approved is the text the admin was looking at
// — the service reads the row, hashes it, and writes both in one call.
func (r *MCPServerRepo) SetToolReview(ctx context.Context, serverID, toolID string, approved, readOnly bool, digest string) error {
	const q = `
		UPDATE mcp_server_tools
		   SET approved = $3, read_only = $4, approved_digest = $5
		 WHERE server_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, serverID, toolID, approved, readOnly, digest)
	if err != nil {
		return fmt.Errorf("review mcp tool: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
