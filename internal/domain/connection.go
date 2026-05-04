package domain

import (
	"context"
	"time"
)

// DBConnection is a tenant-supplied analytical database connection. The DSN
// is stored encrypted at rest; only the database type is in plaintext so the
// agent can dispatch to the right driver.
type DBConnection struct {
	ID           string `json:"id"`
	CompanyID    string `json:"company_id"`
	DBType       string `json:"db_type"` // matches one of internal/adapters/db.Supported
	DSNEncrypted []byte `json:"-"`
	IsDefault    bool   `json:"is_default"`
	Label        string `json:"label,omitempty"`
	// MetabaseDatabaseID links this row to /api/database when db_type is
	// postgres; nil until registration succeeds via Metabase REST API.
	MetabaseDatabaseID *int      `json:"metabase_database_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ConnectionRepository is the persistence contract for tenant DB connections.
type ConnectionRepository interface {
	Create(ctx context.Context, c *DBConnection) error
	GetByID(ctx context.Context, id string) (*DBConnection, error)
	GetDefaultForCompany(ctx context.Context, companyID string) (*DBConnection, error)
	ListByCompany(ctx context.Context, companyID string) ([]*DBConnection, error)
	Update(ctx context.Context, c *DBConnection) error
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, companyID, connectionID string) error
}
