// Package docwarehouse owns the database that published document tables live
// in (T-P6).
//
// **It is a separate database, and the separation is the feature.** The agent
// runs model-written SQL against whatever a source points at. A document source
// pointing at the control database would put `api_keys`,
// `company_llm_credentials` and every other tenant's rows one clever SELECT
// away — the roadmap's Decision 4, and the one place in this track where a
// mistake is catastrophic and silent. So: a different database, a schema per
// company, and a login role per company that holds USAGE on that schema and
// SELECT on its tables and nothing else.
//
// What this package does *not* do is decide anything about a table. Which
// tables exist, what their columns are and whether they may be published are
// `internal/doctable`'s and the review surface's answers; this package takes a
// finished decision and makes it real, in one transaction, replacing whatever
// was there before.
package docwarehouse

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/doctable"
)

// ErrNotConfigured is returned by every method when this deployment has no
// document warehouse.
//
// A supported configuration, like a deployment with no object storage: uploads
// still work, parsing still works, review still works, and publishing refuses
// with a sentence instead of a stack trace. The alternative — publishing into
// the control database when the warehouse DSN is missing — is the failure this
// package exists to make impossible, so it is not a fallback anybody gets by
// forgetting a variable.
var ErrNotConfigured = errors.New("the document warehouse is not configured on this deployment")

// Warehouse is the admin connection to that database.
type Warehouse struct {
	db *sql.DB
	// adminDSN is what `db` dialled. The per-company reader DSN is this one
	// with the credentials swapped, so a deployment that moves the database
	// moves both halves at once and cannot end up with a reader pointed at a
	// host the admin no longer uses.
	adminDSN string
}

// Options configures the warehouse.
type Options struct {
	// DSN is the admin connection: it may create schemas, roles and tables.
	// Empty means this deployment has no document warehouse.
	DSN string
	// MaxOpenConns bounds the pool. Publishing is rare and bursty — a review
	// applies one table — so this is small on purpose.
	MaxOpenConns int
}

// New dials the warehouse, or returns nil when none is configured.
//
// Nil is legal at every call site: the methods are pointer receivers that check
// for it, so a caller holds a `*Warehouse` that may be nil and asks it
// questions rather than branching on configuration in five places.
func New(o Options) (*Warehouse, error) {
	if strings.TrimSpace(o.DSN) == "" {
		return nil, nil
	}
	db, err := sql.Open("postgres", o.DSN)
	if err != nil {
		return nil, fmt.Errorf("open document warehouse: %w", err)
	}
	if o.MaxOpenConns <= 0 {
		o.MaxOpenConns = 4
	}
	db.SetMaxOpenConns(o.MaxOpenConns)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("reach document warehouse: %w", err)
	}
	return &Warehouse{db: db, adminDSN: o.DSN}, nil
}

// Close releases the admin pool.
func (w *Warehouse) Close() error {
	if w == nil || w.db == nil {
		return nil
	}
	return w.db.Close()
}

// Configured reports whether publishing is possible here.
func (w *Warehouse) Configured() bool { return w != nil && w.db != nil }

// Tenant is one company's corner of the warehouse: the schema its tables live
// in, and the DSN the agent reaches them through.
type Tenant struct {
	Schema string
	Role   string
	// DSN authenticates as the reader role. It is returned once, at creation,
	// and stored encrypted in `db_connections` — this package keeps no copy,
	// because a package that could hand out tenant credentials on demand is a
	// package one bug away from handing out the wrong one.
	DSN string
}

// EnsureTenant creates the schema and the reader role for a company if they do
// not exist, and returns the DSN for them.
//
// The role's password is rotated on every call, and the new DSN is what the
// caller stores. That is deliberate: the alternative is reading the existing
// password back out of the control database to reuse it, which means the
// publish path needs the ability to decrypt a DSN it is not otherwise
// interested in. Rotating is cheaper to reason about, and the only cost is that
// a stale cached connection re-dials — which the pool already does when a
// connection row's `updated_at` moves (`internal/adapters/db/pool.go`).
func (w *Warehouse) EnsureTenant(ctx context.Context, companyID string) (*Tenant, error) {
	if !w.Configured() {
		return nil, ErrNotConfigured
	}
	schema := SchemaName(companyID)
	role := schema + "_reader"

	password, err := randomPassword()
	if err != nil {
		return nil, err
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tenant bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdent(schema)); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	// CREATE ROLE has no IF NOT EXISTS, and the DO block is the standard way
	// round it. The password is interpolated rather than bound because DDL does
	// not take parameters — which is why it is generated here from crypto/rand
	// and never comes from a caller.
	createRole := fmt.Sprintf(`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN
        CREATE ROLE %s LOGIN PASSWORD %s;
    ELSE
        ALTER ROLE %s WITH LOGIN PASSWORD %s;
    END IF;
END
$$;`, quoteLiteral(role), quoteIdent(role), quoteLiteral(password), quoteIdent(role), quoteLiteral(password))
	if _, err := tx.ExecContext(ctx, createRole); err != nil {
		return nil, fmt.Errorf("create reader role: %w", err)
	}

	// The grants, and everything they deliberately leave out. USAGE on one
	// schema; SELECT on what is in it and on what will be; and no CREATE
	// anywhere, including `public`, so the role cannot leave an object where
	// another company's role could read it.
	for _, stmt := range []string{
		`GRANT USAGE ON SCHEMA ` + quoteIdent(schema) + ` TO ` + quoteIdent(role),
		`GRANT SELECT ON ALL TABLES IN SCHEMA ` + quoteIdent(schema) + ` TO ` + quoteIdent(role),
		`ALTER DEFAULT PRIVILEGES IN SCHEMA ` + quoteIdent(schema) +
			` GRANT SELECT ON TABLES TO ` + quoteIdent(role),
		`REVOKE CREATE ON SCHEMA public FROM ` + quoteIdent(role),
		`REVOKE ALL ON SCHEMA public FROM ` + quoteIdent(role),
		// The search path means a model writing `SELECT * FROM sales` finds the
		// company's own table without qualifying it, and finds nothing else.
		`ALTER ROLE ` + quoteIdent(role) + ` SET search_path TO ` + quoteIdent(schema),
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("grant on document schema: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tenant bootstrap: %w", err)
	}

	dsn, err := readerDSN(w.adminDSN, role, password)
	if err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "schema": schema}).
		Info("document warehouse tenant ready")
	return &Tenant{Schema: schema, Role: role, DSN: dsn}, nil
}

// Replace creates or replaces one published table and fills it.
//
// Replace, never append. A document re-parsed after a reviewer fixed a column
// type must not double its rows, and an append-with-delete would leave a window
// where the table is half a table — which the agent can query, because nothing
// tells it to wait.
func (w *Warehouse) Replace(
	ctx context.Context, schema, table string, cols []doctable.Column, rows []doctable.Row,
) (int, error) {
	if !w.Configured() {
		return 0, ErrNotConfigured
	}
	if len(cols) == 0 {
		return 0, errors.New("a table with no columns cannot be published")
	}
	table = Identifier(table)

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin publish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qualified := quoteIdent(schema) + "." + quoteIdent(table)
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+qualified); err != nil {
		return 0, fmt.Errorf("drop previous table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, createStatement(qualified, cols)); err != nil {
		return 0, fmt.Errorf("create table: %w", err)
	}

	inserted, err := insertRows(ctx, tx, qualified, cols, rows)
	if err != nil {
		return 0, err
	}

	// The role's default privileges cover tables created after the grant, but
	// only for objects created by the role that ran ALTER DEFAULT PRIVILEGES.
	// Granting explicitly costs one statement and removes a failure mode whose
	// symptom is "permission denied" on a table the reviewer just published.
	role := schema + "_reader"
	if _, err := tx.ExecContext(ctx,
		`GRANT SELECT ON `+qualified+` TO `+quoteIdent(role)); err != nil {
		return 0, fmt.Errorf("grant select on published table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit publish: %w", err)
	}
	return inserted, nil
}

// Drop removes a published table. Used when a draft is withdrawn and by the
// delete path in T-P12, which has to remove all four of the row, the chunks,
// the object and this.
func (w *Warehouse) Drop(ctx context.Context, schema, table string) error {
	if !w.Configured() {
		return ErrNotConfigured
	}
	_, err := w.db.ExecContext(ctx,
		`DROP TABLE IF EXISTS `+quoteIdent(schema)+"."+quoteIdent(Identifier(table)))
	if err != nil {
		return fmt.Errorf("drop published table: %w", err)
	}
	return nil
}

// createStatement builds the DDL for one published table.
//
// Two columns are added that no document contains. `source_page` and
// `source_row` are provenance: an answer built from a PDF can name the page it
// came from without a second lookup, and a suspicious figure is one query away
// from the page that produced it. They are NOT NULL because a row that cannot
// say where it came from is a row nobody can check.
func createStatement(qualified string, cols []doctable.Column) string {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(qualified)
	b.WriteString(" (\n")
	for _, c := range cols {
		b.WriteString("    ")
		b.WriteString(quoteIdent(c.Name))
		b.WriteString(" ")
		b.WriteString(sqlType(c.Type))
		b.WriteString(",\n")
	}
	b.WriteString("    source_page INTEGER NOT NULL,\n")
	b.WriteString("    source_row INTEGER NOT NULL\n)")
	return b.String()
}

// sqlType maps a typed column onto a Postgres type.
//
// NUMERIC rather than DOUBLE PRECISION for every money-shaped column, and it is
// not a preference: a float sum of a hundred rupiah figures is not the number
// the document printed, and this whole track exists because a figure that is
// almost right is the failure nobody catches.
func sqlType(t doctable.ColumnType) string {
	switch t {
	case doctable.ColumnInteger:
		return "BIGINT"
	case doctable.ColumnDecimal, doctable.ColumnCurrency, doctable.ColumnPercentage:
		return "NUMERIC"
	case doctable.ColumnDate:
		return "DATE"
	default:
		return "TEXT"
	}
}

// insertRows writes the data in batches.
//
// One multi-row INSERT per batch rather than one statement per row: a
// three-hundred-row table is one document's worth of work, and three hundred
// round trips inside a transaction is a publish that feels broken on a
// connection with any latency at all.
func insertRows(
	ctx context.Context, tx *sql.Tx, qualified string, cols []doctable.Column, rows []doctable.Row,
) (int, error) {
	const batch = 100
	names := make([]string, 0, len(cols)+2)
	for _, c := range cols {
		names = append(names, quoteIdent(c.Name))
	}
	names = append(names, "source_page", "source_row")
	prefix := "INSERT INTO " + qualified + " (" + strings.Join(names, ", ") + ") VALUES "

	total := 0
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		var (
			placeholders []string
			args         []any
		)
		for _, row := range rows[start:end] {
			marks := make([]string, 0, len(cols)+2)
			for i, c := range cols {
				args = append(args, cellArg(row, i, c))
				marks = append(marks, fmt.Sprintf("$%d", len(args)))
			}
			args = append(args, row.Page)
			marks = append(marks, fmt.Sprintf("$%d", len(args)))
			args = append(args, row.Index)
			marks = append(marks, fmt.Sprintf("$%d", len(args)))
			placeholders = append(placeholders, "("+strings.Join(marks, ", ")+")")
		}
		if len(placeholders) == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, prefix+strings.Join(placeholders, ", "), args...); err != nil {
			return total, fmt.Errorf("insert rows: %w", err)
		}
		total += end - start
	}
	return total, nil
}

// cellArg is one value, as the column's type wants it.
//
// An empty cell is NULL rather than zero. A document that printed nothing means
// nothing, and a zero would be a figure this product invented — which is the
// failure the whole roadmap is arranged around, arriving through the quietest
// possible door.
func cellArg(row doctable.Row, i int, col doctable.Column) any {
	if i >= len(row.Cells) {
		return nil
	}
	cell := row.Cells[i]
	switch {
	case col.Type == doctable.ColumnDate:
		if cell.Date == "" {
			return nil
		}
		return cell.Date
	case col.Type.Numeric():
		if cell.Num == nil {
			return nil
		}
		return *cell.Num
	default:
		if strings.TrimSpace(cell.Raw) == "" {
			return nil
		}
		return cell.Raw
	}
}

// SchemaName is the per-company schema, derived from the company id.
//
// The id rather than the company's name, and the reason is the one this repo
// keeps meeting: tenant-supplied text in an identifier is a decision about
// escaping, and an identifier derived from a uuid is a decision about nothing.
// The first twelve hex characters are unique enough for a schema name and short
// enough to read in a DSN.
func SchemaName(companyID string) string {
	clean := strings.ToLower(strings.ReplaceAll(companyID, "-", ""))
	clean = nonIdent.ReplaceAllString(clean, "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	if clean == "" {
		clean = "unknown"
	}
	return "doc_" + clean
}

var nonIdent = regexp.MustCompile(`[^a-z0-9_]+`)

// Identifier makes a safe, legible table name out of tenant-supplied words.
//
// Allow-listed rather than escaped. Quoting is enough to make a name *safe* and
// not enough to make it *legible*, and this name reaches a model through
// `get_schema`: `"Penjualan Q4 (final!)"` is a table nobody can write SQL
// against by hand, and a model that has to quote every identifier writes worse
// SQL than one that does not.
func Identifier(s string) string {
	out := strings.ToLower(strings.TrimSpace(s))
	out = nonIdentLoose.ReplaceAllString(out, "_")
	out = strings.Trim(out, "_")
	if out == "" {
		return "table_1"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "t_" + out
	}
	const maxIdent = 48
	if len(out) > maxIdent {
		out = strings.Trim(out[:maxIdent], "_")
	}
	return out
}

var nonIdentLoose = regexp.MustCompile(`[^a-z0-9]+`)

// quoteIdent quotes an identifier for DDL. Everything passed to it has already
// been through [Identifier] or [SchemaName]; the quoting is the second layer,
// and the doubling of any embedded quote is the third.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral quotes a string literal for the one place DDL cannot take a
// parameter: CREATE ROLE's password.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// randomPassword is 32 hex characters from crypto/rand. Hex rather than base64
// so it survives a DSN, a shell and a log line unescaped — none of which should
// ever hold it, and one of which eventually will.
func randomPassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate warehouse password: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// readerDSN rewrites the admin DSN's credentials into the reader's.
//
// Derived rather than configured, so a deployment that moves the database moves
// both halves at once. A separately configured reader DSN is a second thing to
// keep in step, and the failure when it drifts is a source that points at the
// old host and answers questions with stale rows.
func readerDSN(adminDSN, role, password string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", fmt.Errorf("parse document warehouse DSN: %w", err)
	}
	if u.Scheme == "" {
		return "", errors.New("the document warehouse DSN must be a URL, e.g. postgres://user:pass@host:5432/db")
	}
	u.User = url.UserPassword(role, password)
	return u.String(), nil
}
