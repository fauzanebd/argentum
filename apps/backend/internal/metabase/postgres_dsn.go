package metabase

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresMetabaseDetails maps a Postgres DSN understood by pgx ConnConfig parsing
// (URI or keyword/libpq-style) into the shape Metabase expects in database details.
//
// References: metabase/driver/postgres `:details` maps (host, port, dbname, user,
// password, ssl, ssl-mode, tunnel-enabled).
func PostgresMetabaseDetails(dsnPlain string) (map[string]interface{}, error) {
	cfg, err := pgconn.ParseConfig(dsnPlain)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	sslMode := cfg.RuntimeParams["sslmode"]
	if sslMode == "" {
		if cfg.TLSConfig != nil {
			sslMode = "require"
		} else {
			sslMode = "disable"
		}
	}
	useSSL := sslMode != "disable" && sslMode != "allow"
	if sslMode == "prefer" && cfg.TLSConfig == nil {
		useSSL = false
	}

	return map[string]interface{}{
		"host":              cfg.Host,
		"port":              port,
		"dbname":            cfg.Database,
		"user":              cfg.User,
		"password":          cfg.Password,
		"ssl":               useSSL,
		"ssl-mode":          sslMode,
		"tunnel-enabled":    false,
		"use-auth-provider": false,
	}, nil
}
