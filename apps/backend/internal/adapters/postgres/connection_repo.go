package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type ConnectionRepo struct{ db *sql.DB }

func NewConnectionRepo(db *sql.DB) *ConnectionRepo { return &ConnectionRepo{db: db} }

const connColumns = `id, company_id, db_type, label, dsn_encrypted, is_default,
		description, description_source, metabase_database_id,
		enable_table_embedding, embeddings_indexed_at, origin,
		allowlist, created_at, updated_at`

func scanConn(row interface {
	Scan(dest ...interface{}) error
}) (*domain.DBConnection, error) {
	c := &domain.DBConnection{}
	var mid sql.NullInt64
	var indexedAt sql.NullTime
	var allowlist []byte
	err := row.Scan(
		&c.ID, &c.CompanyID, &c.DBType, &c.Label, &c.DSNEncrypted, &c.IsDefault,
		&c.Description, &c.DescriptionSource, &mid,
		&c.EnableTableEmbedding, &indexedAt, &c.Origin,
		&allowlist, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	// A malformed allowlist is refused rather than dropped. Ignoring the error
	// would leave `c.Allowlist` at its zero value, which every reader treats as
	// *unrestricted* — so a corrupt column would silently widen what the agent
	// may read, which is the one direction this feature must never fail in.
	if len(allowlist) > 0 {
		if err := json.Unmarshal(allowlist, &c.Allowlist); err != nil {
			return nil, fmt.Errorf("decode allowlist for source %s: %w", c.ID, err)
		}
	}
	if mid.Valid {
		v := int(mid.Int64)
		c.MetabaseDatabaseID = &v
	}
	if indexedAt.Valid {
		t := indexedAt.Time
		c.EmbeddingsIndexedAt = &t
	}
	return c, nil
}

func (r *ConnectionRepo) Create(ctx context.Context, c *domain.DBConnection) error {
	const q = `
		INSERT INTO db_connections
			(company_id, db_type, label, dsn_encrypted, is_default, description, description_source, origin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(NULLIF($8, ''), 'tenant'))
		RETURNING id, created_at, updated_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, c.DBType, c.Label, c.DSNEncrypted, c.IsDefault, c.Description, c.DescriptionSource,
		c.Origin,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("insert connection: %w", err)
	}
	return nil
}

func (r *ConnectionRepo) GetByID(ctx context.Context, id string) (*domain.DBConnection, error) {
	q := `SELECT ` + connColumns + ` FROM db_connections WHERE id = $1`
	c, err := scanConn(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *ConnectionRepo) GetDefaultForCompany(ctx context.Context, companyID string) (*domain.DBConnection, error) {
	q := `SELECT ` + connColumns + ` FROM db_connections WHERE company_id = $1 AND is_default = true LIMIT 1`
	c, err := scanConn(r.db.QueryRowContext(ctx, q, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *ConnectionRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.DBConnection, error) {
	q := `SELECT ` + connColumns + ` FROM db_connections WHERE company_id = $1 ORDER BY created_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.DBConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListAll reads every stored connection, across every tenant.
//
// The one caller is the startup key check (app.LogDSNKeyCoverage): whether the
// `ARGENTUM_DSN_KEY` this process holds opens the rows this database has is a
// question about the deployment, not about a company, and it is answered once
// at boot rather than per request. Deliberately absent from
// domain.ConnectionRepository for that reason — a cross-tenant read has no
// business on the interface the request path holds.
func (r *ConnectionRepo) ListAll(ctx context.Context) ([]*domain.DBConnection, error) {
	q := `SELECT ` + connColumns + ` FROM db_connections ORDER BY created_at`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.DBConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *ConnectionRepo) Update(ctx context.Context, c *domain.DBConnection) error {
	const q = `
		UPDATE db_connections
		SET db_type = $1,
			label = $2,
			dsn_encrypted = $3,
			metabase_database_id = $4,
			description = $5,
			description_source = $6,
			allowlist = $7,
			updated_at = now()
		WHERE id = $8
	`
	var mid interface{}
	if c.MetabaseDatabaseID != nil {
		mid = *c.MetabaseDatabaseID
	}
	// Validated before it is written, not only at the handler. This method
	// writes every column, so it is the last place a caller that loaded a row
	// and changed the label can put an allowlist the readers cannot act on.
	if err := c.Allowlist.Validate(); err != nil {
		return fmt.Errorf("allowlist for source %s: %w", c.ID, err)
	}
	allowlist, err := json.Marshal(c.Allowlist)
	if err != nil {
		return fmt.Errorf("encode allowlist for source %s: %w", c.ID, err)
	}
	_, err = r.db.ExecContext(ctx, q, c.DBType, c.Label, c.DSNEncrypted, mid,
		c.Description, c.DescriptionSource, allowlist, c.ID)
	return err
}

func (r *ConnectionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM db_connections WHERE id = $1`, id)
	return err
}

// MetabaseDatabaseIDForSource returns the Metabase /api/database identifier for
// a specific tenant connection, validating that the connection belongs to the
// given company.
func (r *ConnectionRepo) MetabaseDatabaseIDForSource(ctx context.Context, companyID, sourceID string) (int, error) {
	conn, err := r.GetByID(ctx, sourceID)
	if err != nil {
		return 0, err
	}
	if conn.CompanyID != companyID {
		return 0, domain.ErrUnauthorized
	}
	if conn.MetabaseDatabaseID == nil || *conn.MetabaseDatabaseID == 0 {
		return 0, fmt.Errorf("warehouse not synced to Metabase; add or rotate the DSN so registration can run")
	}
	return *conn.MetabaseDatabaseID, nil
}

// SetEmbeddingToggle flips the embedding-based-table-picker feature on or
// off for a single source. Focused setter so the caller doesn't have to
// round-trip the encrypted DSN through Update.
func (r *ConnectionRepo) SetEmbeddingToggle(ctx context.Context, id string, on bool) error {
	const q = `UPDATE db_connections SET enable_table_embedding = $1, updated_at = now() WHERE id = $2`
	_, err := r.db.ExecContext(ctx, q, on, id)
	return err
}

// MarkEmbeddingsIndexed records the time we last finished a reindex pass
// for a source. The chat runner doesn't read this directly; it's surfaced
// via the API so admins can see embedding age.
func (r *ConnectionRepo) MarkEmbeddingsIndexed(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE db_connections SET embeddings_indexed_at = $1, updated_at = now() WHERE id = $2`
	_, err := r.db.ExecContext(ctx, q, at, id)
	return err
}

// SetDefault marks one connection as default and clears the flag on all
// others for the same company. Run inside a transaction so the partial unique
// index on (company_id) WHERE is_default never sees two true rows.
func (r *ConnectionRepo) SetDefault(ctx context.Context, companyID, connectionID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE db_connections SET is_default = false WHERE company_id = $1`, companyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE db_connections SET is_default = true, updated_at = now() WHERE id = $1 AND company_id = $2`,
		connectionID, companyID); err != nil {
		return err
	}
	return tx.Commit()
}
