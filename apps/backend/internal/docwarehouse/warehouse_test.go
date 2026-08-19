package docwarehouse

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/doctable"
)

// A deployment with no warehouse is a supported one, and every method has to
// say so rather than dereferencing its way into a panic on the publish path.
func TestAnUnconfiguredWarehouseRefusesEveryMethod(t *testing.T) {
	w, err := New(Options{DSN: "  "})
	if err != nil {
		t.Fatalf("New with an empty DSN returned an error: %v", err)
	}
	if w != nil {
		t.Fatal("New with an empty DSN returned a warehouse")
	}
	if w.Configured() {
		t.Fatal("a nil warehouse reports itself configured")
	}
	if _, err := w.EnsureTenant(context.Background(), "co-1"); err != ErrNotConfigured {
		t.Errorf("EnsureTenant = %v, want ErrNotConfigured", err)
	}
	if _, err := w.Replace(context.Background(), "doc_x", "t", nil, nil); err != ErrNotConfigured {
		t.Errorf("Replace = %v, want ErrNotConfigured", err)
	}
	if err := w.Drop(context.Background(), "doc_x", "t"); err != ErrNotConfigured {
		t.Errorf("Drop = %v, want ErrNotConfigured", err)
	}
}

// The schema name comes from the company id and nothing else. A schema named
// after tenant-supplied text would be an escaping decision on every publish.
func TestSchemaNameIsDerivedFromTheIdAlone(t *testing.T) {
	got := SchemaName("6f1e2d3c-4b5a-6978-8a9b-0c1d2e3f4a5b")
	if got != "doc_6f1e2d3c4b5a" {
		t.Errorf("SchemaName = %q", got)
	}
	if SchemaName("") != "doc_unknown" {
		t.Errorf("an empty company id produced %q", SchemaName(""))
	}
	// Whatever arrives, what comes out is an identifier.
	for _, hostile := range []string{`"; DROP SCHEMA public; --`, "../../etc", "CO'1"} {
		name := SchemaName(hostile)
		for _, r := range strings.TrimPrefix(name, "doc_") {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				t.Fatalf("SchemaName(%q) = %q, which is not an identifier", hostile, name)
			}
		}
	}
}

// Table names come from a document's own words, which are tenant-supplied and
// reach a model through get_schema. Allow-listed rather than escaped, for the
// reason the roadmap gives: quoting makes a name safe and not legible.
func TestIdentifierIsSlugSafeAndLegible(t *testing.T) {
	for in, want := range map[string]string{
		"Penjualan Q4 (final!)":   "penjualan_q4_final",
		"  Laporan  Penjualan  ":  "laporan_penjualan",
		"2024 revenue":            "t_2024_revenue",
		`"; DROP TABLE users; --`: "drop_table_users",
		"":                        "table_1",
		"—":                       "table_1",
	} {
		if got := Identifier(in); got != want {
			t.Errorf("Identifier(%q) = %q, want %q", in, got, want)
		}
	}
	long := Identifier(strings.Repeat("penjualan ", 20))
	if len(long) > 48 {
		t.Errorf("a long name was not truncated: %d characters", len(long))
	}
}

// Every published table carries where its rows came from. An answer built from
// a PDF has to be able to name its page without a second lookup.
func TestCreateStatementCarriesProvenanceAndTypes(t *testing.T) {
	ddl := createStatement(`"doc_x"."penjualan"`, []doctable.Column{
		{Name: "bulan", Type: doctable.ColumnText},
		{Name: "unit", Type: doctable.ColumnInteger},
		{Name: "nilai", Type: doctable.ColumnCurrency},
		{Name: "porsi", Type: doctable.ColumnPercentage},
		{Name: "tanggal", Type: doctable.ColumnDate},
	})
	for _, want := range []string{
		`"bulan" TEXT`, `"unit" BIGINT`, `"nilai" NUMERIC`, `"porsi" NUMERIC`, `"tanggal" DATE`,
		"source_page INTEGER NOT NULL", "source_row INTEGER NOT NULL",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL is missing %q:\n%s", want, ddl)
		}
	}
	// NUMERIC, never DOUBLE PRECISION. A float sum of a hundred rupiah figures
	// is not the number the document printed, and this whole track exists
	// because a figure that is almost right is the one nobody catches.
	if strings.Contains(ddl, "DOUBLE PRECISION") || strings.Contains(ddl, "FLOAT") {
		t.Errorf("a money column was typed as a float:\n%s", ddl)
	}
}

// An empty cell is NULL, not zero. A zero would be a figure this product
// invented, arriving through the quietest possible door.
func TestAnEmptyCellIsNullRatherThanZero(t *testing.T) {
	value := 12.0
	row := doctable.Row{Cells: []doctable.Cell{
		{Raw: ""},
		{Raw: "12", Num: &value},
		{Raw: "Jakarta"},
		{Raw: "", Date: ""},
	}}
	cols := []doctable.Column{
		{Name: "kosong", Type: doctable.ColumnInteger},
		{Name: "nilai", Type: doctable.ColumnInteger},
		{Name: "kota", Type: doctable.ColumnText},
		{Name: "tanggal", Type: doctable.ColumnDate},
	}
	if got := cellArg(row, 0, cols[0]); got != nil {
		t.Errorf("an empty numeric cell became %v, want NULL", got)
	}
	if got := cellArg(row, 1, cols[1]); got != 12.0 {
		t.Errorf("a numeric cell became %v, want 12", got)
	}
	if got := cellArg(row, 2, cols[2]); got != "Jakarta" {
		t.Errorf("a text cell became %v", got)
	}
	if got := cellArg(row, 3, cols[3]); got != nil {
		t.Errorf("an empty date became %v, want NULL", got)
	}
	if got := cellArg(row, 9, cols[0]); got != nil {
		t.Errorf("a cell past the end of the row became %v, want NULL", got)
	}
}

// The reader DSN is the admin one with the credentials swapped, so a deployment
// that moves the database moves both halves at once.
func TestReaderDSNKeepsTheHostAndSwapsTheCredentials(t *testing.T) {
	got, err := readerDSN("postgres://admin:secret@docs-db:5432/argentum_docs?sslmode=disable",
		"doc_abc_reader", "deadbeef")
	if err != nil {
		t.Fatalf("readerDSN: %v", err)
	}
	if !strings.Contains(got, "doc_abc_reader:deadbeef@docs-db:5432/argentum_docs") {
		t.Errorf("DSN = %q", got)
	}
	if strings.Contains(got, "admin") || strings.Contains(got, "secret") {
		t.Errorf("the admin credentials survived into the reader DSN: %q", got)
	}
	if !strings.Contains(got, "sslmode=disable") {
		t.Errorf("the connection parameters were dropped: %q", got)
	}
	if _, err := readerDSN("host=docs-db user=admin", "r", "p"); err == nil {
		t.Error("a key/value DSN was accepted; the reader credentials would have been silently lost")
	}
}

func TestPasswordsAreRandomPerCall(t *testing.T) {
	a, err := randomPassword()
	if err != nil {
		t.Fatalf("randomPassword: %v", err)
	}
	b, _ := randomPassword()
	if a == b {
		t.Fatal("two calls produced the same password")
	}
	if len(a) != 32 {
		t.Errorf("password length = %d, want 32 hex characters", len(a))
	}
}

func TestIdentifiersAreQuotedAndEmbeddedQuotesDoubled(t *testing.T) {
	if got := quoteIdent(`we"ird`); got != `"we""ird"` {
		t.Errorf("quoteIdent = %s", got)
	}
	if got := quoteLiteral("O'Brien"); got != "'O''Brien'" {
		t.Errorf("quoteLiteral = %s", got)
	}
}
