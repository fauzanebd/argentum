// Package db defines the multi-database tenant abstraction. Concrete drivers
// (postgres, mysql, …) implement Driver and self-register at process start.
//
// Tools and services never import a specific driver package; they accept a
// Registry (or a TenantConnPool wrapping it) and resolve a Conn per-request
// from the company's configured db_type + DSN.
package db

// Supported database type identifiers. These values appear in the control DB
// `db_connections.db_type` column and in API payloads.
const (
	Postgres  = "postgres"
	MySQL     = "mysql"
	SQLServer = "sqlserver"
)

// Supported is the canonical list of database type identifiers Argentum knows
// how to talk to. Returned by `GET /api/meta/supported-databases`.
var Supported = []string{Postgres, MySQL, SQLServer}

// IsSupported reports whether the given identifier is a registered driver.
func IsSupported(t string) bool {
	for _, s := range Supported {
		if s == t {
			return true
		}
	}
	return false
}
