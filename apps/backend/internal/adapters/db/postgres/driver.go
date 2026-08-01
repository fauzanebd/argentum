// Package postgres is the Postgres implementation of internal/adapters/db.
// Importing this package as a side-effect (`_ "…/postgres"`) registers the
// driver into the package-level db.Registry.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

func init() {
	db.Register(&driver{dialect: dialect{}})
}

type driver struct {
	dialect dialect
}

func (d *driver) Type() string        { return db.Postgres }
func (d *driver) Dialect() db.Dialect { return d.dialect }

func (d *driver) Open(ctx context.Context, dsn string) (db.Conn, error) {
	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &conn{sqlDB: sqlDB, dialect: d.dialect}, nil
}

// dialect implements db.Dialect for Postgres.
type dialect struct{}

func (dialect) Type() string { return db.Postgres }

func (dialect) StatementTimeoutPragma(d time.Duration) string {
	return fmt.Sprintf("SET statement_timeout = '%dms'", d.Milliseconds())
}

func (dialect) ReadOnlyPragma() string { return "SET TRANSACTION READ ONLY" }

func (dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (dialect) Placeholder(n int) string { return "$" + strconv.Itoa(n) }
