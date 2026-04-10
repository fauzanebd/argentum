package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// DB wraps the sql.DB with additional functionality
type DB struct {
	*sql.DB
}

// NewDB creates a new database connection
func NewDB(connStr string) (*DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logrus.Info("Successfully connected to database")
	return &DB{db}, nil
}

// QueryResult represents the result of a query execution
type QueryResult struct {
	Columns []string
	Rows    []map[string]interface{}
	Count   int
}

// ExecuteReadOnly executes a read-only SQL query safely
func (db *DB) ExecuteReadOnly(ctx context.Context, query string) (*QueryResult, error) {
	// Set statement timeout and read-only mode
	query = fmt.Sprintf("SET statement_timeout = '30000'; SET TRANSACTION READ ONLY; %s", query)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Build result
	var resultRows []map[string]interface{}

	for rows.Next() {
		// Create a slice of interface{} to hold the values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert to map
		rowMap := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// Handle byte slices (strings from PostgreSQL)
			if b, ok := val.([]byte); ok {
				rowMap[col] = string(b)
			} else {
				rowMap[col] = val
			}
		}
		resultRows = append(resultRows, rowMap)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return &QueryResult{
		Columns: columns,
		Rows:    resultRows,
		Count:   len(resultRows),
	}, nil
}

// GetSchema returns the database schema metadata
func (db *DB) GetSchema(ctx context.Context) (*SchemaInfo, error) {
	schema := &SchemaInfo{
		Tables: make([]TableInfo, 0),
	}

	// Query for tables
	tableQuery := `
		SELECT table_name 
		FROM information_schema.tables 
		WHERE table_schema = 'public' 
		AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`

	rows, err := db.QueryContext(ctx, tableQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			continue
		}

		tableInfo := TableInfo{
			Name:    tableName,
			Columns: make([]ColumnInfo, 0),
		}

		// Get columns for this table
		columnQuery := `
			SELECT 
				column_name, 
				data_type,
				is_nullable = 'YES' as is_nullable
			FROM information_schema.columns 
			WHERE table_schema = 'public' 
			AND table_name = $1
			ORDER BY ordinal_position
		`

		colRows, err := db.QueryContext(ctx, columnQuery, tableName)
		if err != nil {
			continue
		}

		for colRows.Next() {
			var col ColumnInfo
			if err := colRows.Scan(&col.Name, &col.Type, &col.IsNullable); err != nil {
				continue
			}
			tableInfo.Columns = append(tableInfo.Columns, col)
		}
		colRows.Close()

		schema.Tables = append(schema.Tables, tableInfo)
	}

	return schema, nil
}

// SchemaInfo holds database schema information
type SchemaInfo struct {
	Tables []TableInfo
}

// TableInfo holds table metadata
type TableInfo struct {
	Name        string
	Columns     []ColumnInfo
	Description string
}

// ColumnInfo holds column metadata
type ColumnInfo struct {
	Name       string
	Type       string
	IsNullable bool
}
