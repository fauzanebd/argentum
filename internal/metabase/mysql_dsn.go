package metabase

import (
	"fmt"
	"net"
	"strconv"

	"github.com/go-sql-driver/mysql"
)

// MySQLMetabaseDetails maps a MySQL DSN into the shape Metabase expects in
// database details for the mysql driver.
func MySQLMetabaseDetails(dsnPlain string) (map[string]interface{}, error) {
	cfg, err := mysql.ParseDSN(dsnPlain)
	if err != nil {
		return nil, fmt.Errorf("parse mysql DSN: %w", err)
	}
	host, portStr, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		// Addr may be just a host without port
		host = cfg.Addr
		portStr = "3306"
	}
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 3306
	}
	ssl := cfg.TLSConfig != ""
	return map[string]interface{}{
		"host":           host,
		"port":           port,
		"dbname":         cfg.DBName,
		"user":           cfg.User,
		"password":       cfg.Passwd,
		"ssl":            ssl,
		"tunnel-enabled": false,
	}, nil
}
