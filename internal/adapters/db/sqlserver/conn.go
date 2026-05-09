package sqlserver

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
		// SET LOCK_TIMEOUT failure is non-fatal.
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

// ExtractSchema introspects SQL Server's INFORMATION_SCHEMA, scoped to the
// `dbo` schema (the default schema for most SQL Server applications).
func (c *conn) ExtractSchema(ctx context.Context) (*db.SchemaMetadata, error) {
	out := &db.SchemaMetadata{
		DBType:      db.SQLServer,
		ExtractedAt: time.Now(),
	}

	const tablesQ = `
		SELECT TABLE_NAME, ''
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = 'dbo' AND TABLE_TYPE = 'BASE TABLE'
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
			c.COLUMN_NAME,
			c.DATA_TYPE,
			CASE WHEN c.IS_NULLABLE = 'YES' THEN 1 ELSE 0 END,
			'',
			CASE WHEN pk.COLUMN_NAME IS NOT NULL THEN 1 ELSE 0 END
		FROM INFORMATION_SCHEMA.COLUMNS c
		LEFT JOIN (
			SELECT kcu.TABLE_NAME, kcu.COLUMN_NAME
			FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
			JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
			  ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
			 AND tc.TABLE_SCHEMA   = kcu.TABLE_SCHEMA
			WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
			  AND tc.TABLE_SCHEMA   = 'dbo'
		) pk ON pk.TABLE_NAME = c.TABLE_NAME AND pk.COLUMN_NAME = c.COLUMN_NAME
		WHERE c.TABLE_SCHEMA = 'dbo' AND c.TABLE_NAME = @p1
		ORDER BY c.ORDINAL_POSITION
	`
	rows, err := c.sqlDB.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []db.ColumnInfo
	for rows.Next() {
		var (
			col              db.ColumnInfo
			nullable, isPK   int
		)
		if err := rows.Scan(&col.Name, &col.Type, &nullable, &col.Description, &isPK); err != nil {
			continue
		}
		col.IsNullable = nullable != 0
		col.IsPrimaryKey = isPK != 0
		col.Type = normalizeType(col.Type)
		out = append(out, col)
	}

	const fkQ = `
		SELECT kcu1.COLUMN_NAME, kcu2.TABLE_NAME, kcu2.COLUMN_NAME
		FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS rc
		JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu1
		  ON kcu1.CONSTRAINT_NAME   = rc.CONSTRAINT_NAME
		 AND kcu1.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA
		JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu2
		  ON kcu2.CONSTRAINT_NAME   = rc.UNIQUE_CONSTRAINT_NAME
		 AND kcu2.CONSTRAINT_SCHEMA = rc.UNIQUE_CONSTRAINT_SCHEMA
		 AND kcu2.ORDINAL_POSITION  = kcu1.ORDINAL_POSITION
		WHERE kcu1.TABLE_SCHEMA = 'dbo' AND kcu1.TABLE_NAME = @p1
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
		SELECT kcu1.TABLE_NAME, kcu1.COLUMN_NAME, kcu2.TABLE_NAME, kcu2.COLUMN_NAME
		FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS rc
		JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu1
		  ON kcu1.CONSTRAINT_NAME   = rc.CONSTRAINT_NAME
		 AND kcu1.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA
		JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu2
		  ON kcu2.CONSTRAINT_NAME   = rc.UNIQUE_CONSTRAINT_NAME
		 AND kcu2.CONSTRAINT_SCHEMA = rc.UNIQUE_CONSTRAINT_SCHEMA
		 AND kcu2.ORDINAL_POSITION  = kcu1.ORDINAL_POSITION
		WHERE kcu1.TABLE_SCHEMA = 'dbo'
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
	case "nvarchar", "varchar", "char", "nchar", "text", "ntext", "uniqueidentifier", "xml":
		return "string"
	case "int", "bigint", "smallint", "tinyint":
		return "integer"
	case "decimal", "numeric", "float", "real", "money", "smallmoney":
		return "number"
	case "datetime", "datetime2", "smalldatetime", "date", "time", "datetimeoffset":
		return "datetime"
	case "bit":
		return "boolean"
	default:
		return t
	}
}
