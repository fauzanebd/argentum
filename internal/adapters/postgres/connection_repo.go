package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type ConnectionRepo struct{ db *sql.DB }

func NewConnectionRepo(db *sql.DB) *ConnectionRepo { return &ConnectionRepo{db: db} }

const connColumns = `id, company_id, db_type, label, dsn_encrypted, is_default,
		description, description_source, metabase_database_id, created_at, updated_at`

func scanConn(row interface {
	Scan(dest ...interface{}) error
}) (*domain.DBConnection, error) {
	c := &domain.DBConnection{}
	var mid sql.NullInt64
	err := row.Scan(
		&c.ID, &c.CompanyID, &c.DBType, &c.Label, &c.DSNEncrypted, &c.IsDefault,
		&c.Description, &c.DescriptionSource, &mid, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if mid.Valid {
		v := int(mid.Int64)
		c.MetabaseDatabaseID = &v
	}
	return c, nil
}

func (r *ConnectionRepo) Create(ctx context.Context, c *domain.DBConnection) error {
	const q = `
		INSERT INTO db_connections
			(company_id, db_type, label, dsn_encrypted, is_default, description, description_source)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, c.DBType, c.Label, c.DSNEncrypted, c.IsDefault, c.Description, c.DescriptionSource,
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

func (r *ConnectionRepo) Update(ctx context.Context, c *domain.DBConnection) error {
	const q = `
		UPDATE db_connections
		SET db_type = $1,
			label = $2,
			dsn_encrypted = $3,
			metabase_database_id = $4,
			description = $5,
			description_source = $6,
			updated_at = now()
		WHERE id = $7
	`
	var mid interface{}
	if c.MetabaseDatabaseID != nil {
		mid = *c.MetabaseDatabaseID
	}
	_, err := r.db.ExecContext(ctx, q, c.DBType, c.Label, c.DSNEncrypted, mid, c.Description, c.DescriptionSource, c.ID)
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
