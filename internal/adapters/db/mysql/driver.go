// Package mysql is the MySQL implementation of internal/adapters/db.
// Importing this package as a side-effect (`_ "…/mysql"`) registers the
// driver into the package-level db.Registry.
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

func init() {
	db.Register(&driver{dialect: dialect{}})
}

type driver struct {
	dialect dialect
}

func (d *driver) Type() string         { return db.MySQL }
func (d *driver) Dialect() db.Dialect { return d.dialect }

func (d *driver) Open(ctx context.Context, dsn string) (db.Conn, error) {
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}
	return &conn{sqlDB: sqlDB, dialect: d.dialect}, nil
}

type dialect struct{}

func (dialect) Type() string { return db.MySQL }

func (dialect) StatementTimeoutPragma(d time.Duration) string {
	// MySQL 5.7.4+ supports per-session max_execution_time in milliseconds.
	return fmt.Sprintf("SET SESSION MAX_EXECUTION_TIME = %d", d.Milliseconds())
}

func (dialect) ReadOnlyPragma() string {
	return "SET SESSION TRANSACTION READ ONLY"
}

func (dialect) QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
