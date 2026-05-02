package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

const queryTimeout = 30 * time.Second

type conn struct {
	sqlDB   *sql.DB
	dialect dialect
}

func (c *conn) Ping(ctx context.Context) error { return c.sqlDB.PingContext(ctx) }
func (c *conn) Close() error                   { return c.sqlDB.Close() }

func (c *conn) ExecuteReadOnly(ctx context.Context, query string) (*db.QueryResult, error) {
	tx, err := c.sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, c.dialect.StatementTimeoutPragma(queryTimeout)); err != nil {
		// Older MySQL versions reject MAX_EXECUTION_TIME; non-fatal.
		_ = err
	}

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}

	result := &db.QueryResult{Columns: cols, Rows: []map[string]interface{}{}}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		row := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = v
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	result.Count = len(result.Rows)
	return result, nil
}

// ExtractSchema introspects MySQL's information_schema, scoping to the
// connection's current database (DATABASE()).
func (c *conn) ExtractSchema(ctx context.Context) (*db.SchemaMetadata, error) {
	out := &db.SchemaMetadata{
		DBType:      db.MySQL,
		ExtractedAt: time.Now(),
	}

	const tablesQ = `
		SELECT TABLE_NAME, COALESCE(TABLE_COMMENT, '')
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME
	`
	rows, err := c.sqlDB.QueryContext(ctx, tablesQ)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, desc string
		if err := rows.Scan(&name, &desc); err != nil {
			continue
		}
		t := db.TableInfo{Name: name, Description: desc}
		cols, err := c.columns(ctx, name)
		if err == nil {
			t.Columns = cols
		}
		out.Tables = append(out.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rels, err := c.relationships(ctx)
	if err == nil {
		out.Relationships = rels
	}
	return out, nil
}

func (c *conn) columns(ctx context.Context, table string) ([]db.ColumnInfo, error) {
	const q = `
		SELECT
			COLUMN_NAME,
			DATA_TYPE,
			IS_NULLABLE = 'YES',
			COALESCE(COLUMN_COMMENT, ''),
			COLUMN_KEY = 'PRI'
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION
	`
	rows, err := c.sqlDB.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []db.ColumnInfo
	for rows.Next() {
		var col db.ColumnInfo
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &col.Description, &col.IsPrimaryKey); err != nil {
			continue
		}
		col.Type = normalizeType(col.Type)
		out = append(out, col)
	}

	const fkQ = `
		SELECT COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL
	`
	fkRows, err := c.sqlDB.QueryContext(ctx, fkQ, table)
	if err == nil {
		defer fkRows.Close()
		for fkRows.Next() {
			var col, ftable, fcol string
			if err := fkRows.Scan(&col, &ftable, &fcol); err != nil {
				continue
			}
			for i := range out {
				if out[i].Name == col {
					out[i].IsForeignKey = true
					out[i].ForeignKeyTable = ftable
					out[i].ForeignKeyColumn = fcol
					break
				}
			}
		}
	}
	return out, rows.Err()
}

func (c *conn) relationships(ctx context.Context) ([]db.Relationship, error) {
	const q = `
		SELECT TABLE_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE() AND REFERENCED_TABLE_NAME IS NOT NULL
	`
	rows, err := c.sqlDB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Relationship
	for rows.Next() {
		var r db.Relationship
		if err := rows.Scan(&r.FromTable, &r.FromColumn, &r.ToTable, &r.ToColumn); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func normalizeType(t string) string {
	switch t {
	case "varchar", "char", "text", "tinytext", "mediumtext", "longtext", "enum", "set":
		return "string"
	case "int", "tinyint", "smallint", "mediumint", "bigint", "year":
		return "integer"
	case "decimal", "float", "double", "numeric":
		return "number"
	case "datetime", "timestamp", "date", "time":
		return "datetime"
	case "bool", "boolean":
		return "boolean"
	default:
		return t
	}
}
