package db

import (
	"context"
	"time"
)

// Driver is the entry point for talking to a tenant analytical database. Each
// supported database type has exactly one Driver implementation.
type Driver interface {
	// Type returns the canonical identifier (one of the constants in this
	// package).
	Type() string

	// Open establishes a connection pool against the given DSN. The returned
	// Conn owns its underlying *sql.DB and must be closed when the tenant's
	// configuration changes or it is evicted from the cache.
	Open(ctx context.Context, dsn string) (Conn, error)

	// Dialect returns the per-driver SQL fragments needed by the schema
	// extractor and the read-only enforcer.
	Dialect() Dialect
}

// Conn is a connection pool to a single tenant analytical database. It
// presents a narrow, agent-safe interface: read-only query execution and
// schema introspection. There is intentionally no mutation API.
type Conn interface {
	// ExecuteReadOnly runs the supplied SQL inside a read-only transaction
	// with a server-enforced statement timeout. Returns ordered columns +
	// row maps.
	ExecuteReadOnly(ctx context.Context, sql string) (*QueryResult, error)

	// ExtractSchema introspects the database's information_schema (or the
	// driver's equivalent) and returns table + column + relationship metadata
	// for the agent's `get_schema` tool.
	ExtractSchema(ctx context.Context) (*SchemaMetadata, error)

	// Ping verifies the connection is still healthy.
	Ping(ctx context.Context) error

	// Close releases the underlying pool.
	Close() error
}

// Dialect is the bag of database-specific SQL fragments and behaviours used
// by drivers. Centralising it here keeps driver code thin and makes adding a
// new database mostly a matter of writing a Dialect.
type Dialect interface {
	// Type identifies the dialect; matches the driver's Type().
	Type() string

	// StatementTimeoutPragma returns the SQL preamble to install a per-query
	// statement timeout on the active session.
	StatementTimeoutPragma(d time.Duration) string

	// ReadOnlyPragma returns the SQL preamble to put the active transaction
	// into read-only mode.
	ReadOnlyPragma() string

	// QuoteIdentifier wraps a column / table identifier in the appropriate
	// quoting characters for this dialect.
	QuoteIdentifier(name string) string
}

// QueryResult is the canonical return type of read-only queries across all
// drivers.
type QueryResult struct {
	Columns []string                 `json:"columns"`
	Rows    []map[string]interface{} `json:"rows"`
	Count   int                      `json:"count"`
}

// SchemaMetadata is the canonical schema-introspection return type across all
// drivers.
type SchemaMetadata struct {
	DBType        string         `json:"db_type"`
	ExtractedAt   time.Time      `json:"extracted_at"`
	Tables        []TableInfo    `json:"tables"`
	Relationships []Relationship `json:"relationships"`
}

// TableInfo describes one table in a tenant schema.
type TableInfo struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Columns     []ColumnInfo `json:"columns"`
}

// ColumnInfo describes one column in a tenant schema.
type ColumnInfo struct {
	Name             string `json:"name"`
	Type             string `json:"type"` // normalized: string|integer|number|datetime|boolean|other
	Description      string `json:"description,omitempty"`
	IsNullable       bool   `json:"is_nullable"`
	IsPrimaryKey     bool   `json:"is_primary_key"`
	IsForeignKey     bool   `json:"is_foreign_key"`
	ForeignKeyTable  string `json:"foreign_key_table,omitempty"`
	ForeignKeyColumn string `json:"foreign_key_column,omitempty"`
}

// Relationship is a foreign-key relationship between two tables.
type Relationship struct {
	FromTable  string `json:"from_table"`
	FromColumn string `json:"from_column"`
	ToTable    string `json:"to_table"`
	ToColumn   string `json:"to_column"`
}
