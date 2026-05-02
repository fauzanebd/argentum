package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// queryTimeout is the per-query statement timeout enforced by the SET
// statement_timeout pragma below. Applies regardless of context deadline.
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
	defer tx.Rollback() // read-only; safe to always rollback

	if _, err := tx.ExecContext(ctx, c.dialect.StatementTimeoutPragma(queryTimeout)); err != nil {
		return nil, fmt.Errorf("set statement_timeout: %w", err)
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

func (c *conn) ExtractSchema(ctx context.Context) (*db.SchemaMetadata, error) {
	out := &db.SchemaMetadata{
		DBType:      db.Postgres,
		ExtractedAt: time.Now(),
	}

	const tablesQ = `
		SELECT t.table_name, COALESCE(obj_description(pgc.oid, 'pg_class'), '')
		FROM information_schema.tables t
		JOIN pg_class pgc ON pgc.relname = t.table_name
		WHERE t.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		ORDER BY t.table_name
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
			c.column_name,
			c.data_type,
			c.is_nullable = 'YES',
			COALESCE(col_description(pgc.oid, c.ordinal_position), ''),
			CASE WHEN pk.column_name IS NOT NULL THEN true ELSE false END AS is_pk
		FROM information_schema.columns c
		JOIN pg_class pgc ON pgc.relname = c.table_name
		LEFT JOIN (
			SELECT kcu.column_name, kcu.table_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
				ON tc.constraint_name = kcu.constraint_name
			WHERE tc.constraint_type = 'PRIMARY KEY'
		) pk ON pk.column_name = c.column_name AND pk.table_name = c.table_name
		WHERE c.table_schema = 'public' AND c.table_name = $1
		ORDER BY c.ordinal_position
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
		SELECT kcu.column_name, ccu.table_name, ccu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage ccu
			ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name = $1
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
		SELECT tc.table_name, kcu.column_name, ccu.table_name, ccu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage ccu
			ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'public'
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
	case "character varying", "varchar", "character", "char", "text":
		return "string"
	case "integer", "bigint", "smallint", "serial", "bigserial":
		return "integer"
	case "numeric", "decimal", "real", "double precision":
		return "number"
	case "timestamp without time zone", "timestamp with time zone", "date", "time":
		return "datetime"
	case "boolean":
		return "boolean"
	default:
		return t
	}
}
