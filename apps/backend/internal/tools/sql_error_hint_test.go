package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// stubSchema is the cached schema a failed query answers itself from.
type stubSchema struct {
	meta  *db.SchemaMetadata
	err   error
	calls int
}

func (s *stubSchema) FetchSchema(context.Context, string, string, bool) (*db.SchemaMetadata, error) {
	s.calls++
	return s.meta, s.err
}

// The tenant this was written against: SQL Server, Indonesian table names, and
// a payment-split column family the model guessed two wrong members of.
func salesSchema() *db.SchemaMetadata {
	return &db.SchemaMetadata{
		DBType: "sqlserver",
		Tables: []db.TableInfo{
			{Name: "tbTr_Penjualan_S", Columns: []db.ColumnInfo{
				{Name: "TGL"}, {Name: "KDCAB"}, {Name: "JLS_SJLH"},
				{Name: "JLS_SJLH_TUNAI"}, {Name: "JLS_SJLH_DEBIT"},
			}},
			{Name: "tbTr_Penjualan_H_Daily", Columns: []db.ColumnInfo{
				{Name: "NOFAK"}, {Name: "TGL"}, {Name: "TOTAL"},
			}},
			{Name: "tbMs_Barang", Columns: []db.ColumnInfo{{Name: "PRDCD"}}},
		},
	}
}

func hintFor(t *testing.T, sql string, cause error) string {
	t.Helper()
	err := explainSQLError(context.Background(), &stubSchema{meta: salesSchema()},
		"co_1", "src_1", sql, cause)
	return err.Error()
}

// The failure this exists for: a wrong column cost the turn the call that
// failed AND the get_schema call it took to recover. On a twelve-call budget
// that is what ran the live report out of room before the PDF.
func TestBadColumnAnswersWithTheRealColumns(t *testing.T) {
	got := hintFor(t,
		"SELECT SUM(JLS_SJLH_QRIS) FROM tbTr_Penjualan_S WHERE TGL >= '2026-01-01'",
		errors.New("mssql: Invalid column name 'JLS_SJLH_QRIS'."))

	if !strings.Contains(got, "query execution failed") {
		t.Errorf("the driver's own error was dropped: %q", got)
	}
	for _, col := range []string{"JLS_SJLH_TUNAI", "JLS_SJLH_DEBIT", "TGL"} {
		if !strings.Contains(got, col) {
			t.Errorf("hint omits real column %s: %q", col, got)
		}
	}
	// Scoped to the table the query read. Quoting every column of every table
	// would cost more context than the get_schema call this replaces.
	if strings.Contains(got, "PRDCD") {
		t.Errorf("hint leaked columns of a table the query never referenced: %q", got)
	}
	// The near miss is what the model actually needs: it guessed a member of a
	// column family that exists.
	if !strings.Contains(got, "closest to") {
		t.Errorf("hint offers no suggestion for the name that failed: %q", got)
	}
}

func TestBadTableAnswersWithTableNames(t *testing.T) {
	got := hintFor(t,
		"SELECT * FROM tbTr_Penjualan_Harian",
		errors.New("mssql: Invalid object name 'tbTr_Penjualan_Harian'."))

	if !strings.Contains(got, "tbTr_Penjualan_H_Daily") || !strings.Contains(got, "tbMs_Barang") {
		t.Errorf("hint does not list the source's tables: %q", got)
	}
	// A table that does not exist is no reason to hand over every column of
	// every table that does.
	if strings.Contains(got, "JLS_SJLH_TUNAI") {
		t.Errorf("a missing-table hint spent context on columns: %q", got)
	}
}

// Every dialect this codebase connects to, in its own words.
func TestNameErrorsAreRecognisedPerDialect(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		wantKind nameErrorKind
		wantName string
	}{
		{"mssql column", "mssql: Invalid column name 'JLS_SJLH_QRIS'.", nameErrorColumn, "JLS_SJLH_QRIS"},
		{"mssql object", "mssql: Invalid object name 'dbo.tbFoo'.", nameErrorTable, "tbFoo"},
		{"mysql column", "Error 1054: Unknown column 'qris' in 'field list'", nameErrorColumn, "qris"},
		{"mysql table", "Error 1146: Table 'sales.tbFoo' doesn't exist", nameErrorTable, "tbFoo"},
		{"postgres column", `pq: column "qris" does not exist`, nameErrorColumn, "qris"},
		{"postgres relation", `pq: relation "tbfoo" does not exist`, nameErrorTable, "tbfoo"},
		{"sqlite column", "SQL logic error: no such column: qris", nameErrorColumn, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, name, ok := parseNameError(tt.err)
			if tt.wantName == "" {
				// SQLite quotes nothing; there is no name to hand back and the
				// driver error is left alone rather than guessed at.
				if ok {
					t.Fatalf("parsed an unquoted message as %q", name)
				}
				return
			}
			if !ok {
				t.Fatalf("not recognised as a name error: %q", tt.err)
			}
			if kind != tt.wantKind {
				t.Errorf("kind = %v, want %v", kind, tt.wantKind)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

// A hint attached to a syntax error or a timeout is context spent saying
// nothing, and reads as if the name were the problem.
func TestOtherFailuresKeepTheDriversOwnMessage(t *testing.T) {
	for _, msg := range []string{
		"mssql: Incorrect syntax near 'FORM'.",
		"context deadline exceeded",
		"mssql: Conversion failed when converting the varchar value 'x' to data type int.",
		"pq: permission denied for table tbTr_Penjualan_S",
	} {
		schema := &stubSchema{meta: salesSchema()}
		err := explainSQLError(context.Background(), schema, "co_1", "src_1",
			"SELECT 1 FROM tbTr_Penjualan_S", errors.New(msg))
		if !strings.Contains(err.Error(), msg) {
			t.Errorf("driver message lost: %q", err)
		}
		if strings.Contains(err.Error(), "JLS_SJLH_TUNAI") {
			t.Errorf("a column list was attached to %q: %q", msg, err)
		}
	}
}

// No schema provider, or a schema that cannot be read, must leave the query's
// own failure exactly as it was.
func TestHintDegradesToTheBareError(t *testing.T) {
	cause := errors.New("mssql: Invalid column name 'QRIS'.")
	sql := "SELECT QRIS FROM tbTr_Penjualan_S"

	if err := explainSQLError(context.Background(), nil, "co_1", "src_1", sql, cause); !errors.Is(err, cause) {
		t.Errorf("nil provider changed the error: %q", err)
	}
	broken := &stubSchema{err: errors.New("source unreachable")}
	err := explainSQLError(context.Background(), broken, "co_1", "src_1", sql, cause)
	if !errors.Is(err, cause) {
		t.Errorf("unwrapping lost the driver error: %q", err)
	}
	if strings.Contains(err.Error(), "source unreachable") {
		t.Errorf("the failed lookup leaked into the query's error: %q", err)
	}
}

func TestTablesInSQL(t *testing.T) {
	got := tablesInSQL(`SELECT s.TGL, h.TOTAL
		FROM dbo.tbTr_Penjualan_S s
		JOIN [tbTr_Penjualan_H_Daily] h ON h.TGL = s.TGL
		WHERE s.KDCAB = '0201'`)

	for _, want := range []string{"tbtr_penjualan_s", "tbtr_penjualan_h_daily"} {
		if !got[want] {
			t.Errorf("%q not found in %v", want, got)
		}
	}
}

// A wide table must not turn a failed query into a context dump.
func TestHintIsCapped(t *testing.T) {
	wide := make([]db.ColumnInfo, 0, maxHintColumns*3)
	for i := 0; i < maxHintColumns*3; i++ {
		wide = append(wide, db.ColumnInfo{Name: strings.Repeat("c", 12)})
	}
	schema := &stubSchema{meta: &db.SchemaMetadata{
		DBType: "sqlserver",
		Tables: []db.TableInfo{{Name: "wide", Columns: wide}},
	}}
	err := explainSQLError(context.Background(), schema, "co_1", "src_1",
		"SELECT nope FROM wide", errors.New("mssql: Invalid column name 'nope'."))

	if len(err.Error()) > maxHintChars+len("query execution failed: ")+200 {
		t.Errorf("hint is %d chars, past the cap", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "more, call get_schema") {
		t.Errorf("truncation is silent — the model cannot tell the list is partial: %q", err)
	}
}

// The lookup goes through the cache the agent already warmed, never a forced
// re-introspection: this runs on the failure path of a query the tenant's
// database has already paid a round trip for.
func TestHintNeverForcesReintrospection(t *testing.T) {
	var forced bool
	schema := forceRecorder{meta: salesSchema(), sawForce: &forced}
	_ = explainSQLError(context.Background(), schema, "co_1", "src_1",
		"SELECT nope FROM tbTr_Penjualan_S", errors.New("mssql: Invalid column name 'nope'."))
	if forced {
		t.Error("the hint forced a schema refresh on a failing query")
	}
}

type forceRecorder struct {
	meta     *db.SchemaMetadata
	sawForce *bool
}

func (f forceRecorder) FetchSchema(_ context.Context, _, _ string, force bool) (*db.SchemaMetadata, error) {
	if force {
		*f.sawForce = true
	}
	return f.meta, nil
}
