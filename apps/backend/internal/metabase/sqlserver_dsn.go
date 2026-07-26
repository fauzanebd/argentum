package metabase

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// SQLServerMetabaseDetails maps a SQL Server DSN (URL form, as built by the
// HTTP handler — `sqlserver://user:pass@host:port?database=...&encrypt=...`)
// into the shape Metabase expects in database details for the sqlserver
// driver.
//
// References: metabase/driver/sqlserver `connection-properties` —
// host, port, db, user, password, instance, domain, ssl,
// rewrite-batched-updates, tunnel-enabled.
func SQLServerMetabaseDetails(dsnPlain string) (map[string]interface{}, error) {
	u, err := url.Parse(dsnPlain)
	if err != nil {
		return nil, fmt.Errorf("parse sqlserver DSN: %w", err)
	}
	if u.Scheme != "sqlserver" {
		return nil, fmt.Errorf("parse sqlserver DSN: unexpected scheme %q", u.Scheme)
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
		portStr = "1433"
	}
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 1433
	}

	q := u.Query()
	dbname := q.Get("database")
	encrypt := strings.ToLower(q.Get("encrypt"))
	ssl := encrypt != "disable" && encrypt != "false"

	user := ""
	password := ""
	if u.User != nil {
		user = u.User.Username()
		password, _ = u.User.Password()
	}

	return map[string]interface{}{
		"host":                    host,
		"port":                    port,
		"db":                      dbname,
		"user":                    user,
		"password":                password,
		"instance":                "",
		"domain":                  "",
		"ssl":                     ssl,
		"rewrite-batched-updates": false,
		"tunnel-enabled":          false,
	}, nil
}
