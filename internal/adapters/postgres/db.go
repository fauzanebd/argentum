// Package postgres holds the control-plane Postgres repository
// implementations and the connection helper. Tenant-DB Postgres access lives
// at internal/adapters/db/postgres — these are different concerns.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// New opens a *sql.DB against the Argentum control-plane Postgres URL and
// pings it. Use the resulting handle to construct each X-Repository.
func New(connStr string) (*sql.DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open control db: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping control db: %w", err)
	}
	logrus.Info("Connected to control-plane Postgres")
	return db, nil
}
