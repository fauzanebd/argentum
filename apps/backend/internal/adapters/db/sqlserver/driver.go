// Package sqlserver is the Microsoft SQL Server implementation of
// internal/adapters/db. Importing this package as a side-effect
// (`_ "…/sqlserver"`) registers the driver into the package-level
// db.Registry.
package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

func init() {
	db.Register(&driver{dialect: dialect{}})
}

type driver struct {
	dialect dialect
}

func (d *driver) Type() string        { return db.SQLServer }
func (d *driver) Dialect() db.Dialect { return d.dialect }

func (d *driver) Open(ctx context.Context, dsn string) (db.Conn, error) {
	sqlDB, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlserver open: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sqlserver ping: %w", err)
	}
	return &conn{sqlDB: sqlDB, dialect: d.dialect}, nil
}

// dialect implements db.Dialect for SQL Server.
type dialect struct{}

func (dialect) Type() string { return db.SQLServer }

// StatementTimeoutPragma returns SET LOCK_TIMEOUT — the closest T-SQL
// equivalent. It only governs lock-wait time, not query execution time;
// runaway queries are killed via context cancellation (go-mssqldb sends a
// TDS attention packet on ctx deadline).
func (dialect) StatementTimeoutPragma(d time.Duration) string {
	return fmt.Sprintf("SET LOCK_TIMEOUT %d", d.Milliseconds())
}

// ReadOnlyPragma is set for interface completeness. Read-only enforcement
// comes from sql.TxOptions{ReadOnly:true} + the customer's least-privilege
// login (recommend db_datareader only).
func (dialect) ReadOnlyPragma() string { return "SET TRANSACTION ISOLATION LEVEL SNAPSHOT" }

func (dialect) QuoteIdentifier(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// Placeholder is `@pN` — go-mssqldb's named-parameter syntax, which the driver
// matches to sql.Named or positional args. The renderer appends one arg per
// occurrence, so `@p1`, `@p2`, … each get their own value.
func (dialect) Placeholder(n int) string { return "@p" + strconv.Itoa(n) }
