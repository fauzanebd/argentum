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

func (r *ConnectionRepo) Create(ctx context.Context, c *domain.DBConnection) error {
	const q = `
		INSERT INTO db_connections (company_id, db_type, label, dsn_encrypted, is_default)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, c.DBType, c.Label, c.DSNEncrypted, c.IsDefault,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("insert connection: %w", err)
	}
	return nil
}

func (r *ConnectionRepo) GetByID(ctx context.Context, id string) (*domain.DBConnection, error) {
	const q = `
		SELECT id, company_id, db_type, label, dsn_encrypted, is_default, metabase_database_id, created_at, updated_at
		FROM db_connections WHERE id = $1
	`
	c := &domain.DBConnection{}
	var mid sql.NullInt64
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&c.ID, &c.CompanyID, &c.DBType, &c.Label, &c.DSNEncrypted, &c.IsDefault, &mid, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == nil && mid.Valid {
		v := int(mid.Int64)
		c.MetabaseDatabaseID = &v
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *ConnectionRepo) GetDefaultForCompany(ctx context.Context, companyID string) (*domain.DBConnection, error) {
	const q = `
		SELECT id, company_id, db_type, label, dsn_encrypted, is_default, metabase_database_id, created_at, updated_at
		FROM db_connections WHERE company_id = $1 AND is_default = true LIMIT 1
	`
	c := &domain.DBConnection{}
	var mid sql.NullInt64
	err := r.db.QueryRowContext(ctx, q, companyID).Scan(
		&c.ID, &c.CompanyID, &c.DBType, &c.Label, &c.DSNEncrypted, &c.IsDefault, &mid, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == nil && mid.Valid {
		v := int(mid.Int64)
		c.MetabaseDatabaseID = &v
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *ConnectionRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.DBConnection, error) {
	const q = `
		SELECT id, company_id, db_type, label, dsn_encrypted, is_default, metabase_database_id, created_at, updated_at
		FROM db_connections WHERE company_id = $1 ORDER BY created_at
	`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.DBConnection
	for rows.Next() {
		c := &domain.DBConnection{}
		var mid sql.NullInt64
		if err := rows.Scan(
			&c.ID, &c.CompanyID, &c.DBType, &c.Label, &c.DSNEncrypted, &c.IsDefault, &mid, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if mid.Valid {
			v := int(mid.Int64)
			c.MetabaseDatabaseID = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *ConnectionRepo) Update(ctx context.Context, c *domain.DBConnection) error {
	const q = `
		UPDATE db_connections
		SET db_type = $1, label = $2, dsn_encrypted = $3, metabase_database_id = $4, updated_at = now()
		WHERE id = $5
	`
	var mid interface{}
	if c.MetabaseDatabaseID != nil {
		mid = *c.MetabaseDatabaseID
	}
	_, err := r.db.ExecContext(ctx, q, c.DBType, c.Label, c.DSNEncrypted, mid, c.ID)
	return err
}

func (r *ConnectionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM db_connections WHERE id = $1`, id)
	return err
}

// DefaultPostgresMetabaseDatabaseID returns the Metabase /api/database identifier for the
// company's default postgres analytical connection.
func (r *ConnectionRepo) DefaultPostgresMetabaseDatabaseID(ctx context.Context, companyID string) (int, error) {
	conn, err := r.GetDefaultForCompany(ctx, companyID)
	if err != nil {
		return 0, err
	}
	if conn.DBType != "postgres" {
		return 0, fmt.Errorf("default analytical connection must be postgres for metabase dashboards (got %s)", conn.DBType)
	}
	if conn.MetabaseDatabaseID == nil || *conn.MetabaseDatabaseID == 0 {
		return 0, fmt.Errorf("warehouse not synced to Metabase; add or rotate the Postgres DSN so registration can run")
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
