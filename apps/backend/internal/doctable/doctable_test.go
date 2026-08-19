package doctable

import (
	"math"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/docparse"
)

// page builds one text page holding one table candidate, which is the shape
// every fixture here needs and the shape the parser produces.
func page(no int, rows [][]string, opts ...func(*docparse.Page)) docparse.Page {
	p := docparse.Page{
		Number: no,
		Kind:   docparse.KindText,
		Width:  595,
		Height: 842,
		Tables: []docparse.Table{{
			Strategy: "lines",
			BBox:     []float64{50, 120, 545, 400},
			Rows:     rows,
			RowCount: len(rows),
			ColCount: len(rows[0]),
		}},
	}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func firstTable(t *testing.T, pages ...docparse.Page) Table {
	t.Helper()
	out := Build(pages, Options{})
	if len(out) != 1 {
		t.Fatalf("got %d tables, want 1", len(out))
	}
	return out[0]
}

func num(t *testing.T, table Table, row, col int) float64 {
	t.Helper()
	if row >= len(table.Rows) {
		t.Fatalf("row %d does not exist; the table has %d", row, len(table.Rows))
	}
	cell := table.Rows[row].Cells[col]
	if cell.Num == nil {
		t.Fatalf("row %d column %d (%q) did not type as a number", row, col, cell.Raw)
	}
	return *cell.Num
}

// The seven failure families, in the shapes a real document writes them. Every
// one of these produces a number that is wrong and looks right, which is why
// they are a table test rather than a gate: the live half cannot tell 1,234
// from 1.234 either.
func TestCellsParseInBothLocalesAndAllTheirDisguises(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		dec  byte
		want float64
	}{
		{"indonesian full form", "1.234.567,89", ',', 1234567.89},
		{"english full form", "1,234,567.89", '.', 1234567.89},
		{"accounting negative", "(1.234)", ',', -1234},
		{"accounting negative with currency inside", "(Rp 1.234)", ',', -1234},
		{"footnote marker", "1.234²", ',', 1234},
		{"asterisk footnote", "1.234*", ',', 1234},
		{"cell-level magnitude word", "Rp 1,2 juta", ',', 1200000},
		{"trailing minus, an ERP export", "1.234-", ',', -1234},
		{"currency prefix", "Rp 1.234.567", ',', 1234567},
		{"percentage", "12,5%", ',', 12.5},
		{"non-breaking space as a separator", "1 234 567", ',', 1234567},
	} {
		got, ok := readCell(cleanText(tc.raw), tc.dec)
		if !ok {
			t.Errorf("%s: %q was refused", tc.name, tc.raw)
			continue
		}
		if math.Abs(got.value-tc.want) > 0.001 {
			t.Errorf("%s: %q = %v, want %v", tc.name, tc.raw, got.value, tc.want)
		}
	}

	// And the other half of the contract: text is refused rather than coerced.
	for _, raw := range []string{"Jakarta", "n/a", "-", "TOTAL", ""} {
		if _, ok := readCell(cleanText(raw), ','); ok {
			t.Errorf("%q was read as a number", raw)
		}
	}
}

// A column decides its own separator, and the decision is what makes "1.234"
// mean one thing in one table and another in the next.
func TestAColumnDecidesItsSeparatorAndTheCellsFollow(t *testing.T) {
	indonesian := firstTable(t, page(1, [][]string{
		{"Produk", "Penjualan"},
		{"Kopi", "1.234"},
		{"Teh", "12.500"},
		{"Gula", "1.234.567"},
	}))
	if got := num(t, indonesian, 0, 1); got != 1234 {
		t.Errorf("in a column of Indonesian groups, \"1.234\" = %v, want 1234", got)
	}

	english := firstTable(t, page(1, [][]string{
		{"Product", "Margin"},
		{"Coffee", "1.234"},
		{"Tea", "0.75"},
		{"Sugar", "12.5"},
	}))
	if got := num(t, english, 0, 1); math.Abs(got-1.234) > 1e-9 {
		t.Errorf("in a column of English decimals, \"1.234\" = %v, want 1.234", got)
	}
}

// Acceptance: a column whose header carries "dalam jutaan" yields values
// multiplied by 10⁶, and the multiplier is recorded on the column.
func TestAScaleWordMultipliesTheColumnAndSaysSo(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Penjualan (dalam jutaan Rupiah)"},
		{"Oktober", "3.377"},
		{"November", "3.708"},
		{"Desember", "3.863"},
	}))

	col := table.Columns[1]
	if col.Multiplier != 1e6 {
		t.Fatalf("multiplier = %v, want 1e6 (source %q)", col.Multiplier, col.MultiplierSource)
	}
	if !strings.Contains(strings.ToLower(col.MultiplierSource), "jutaan") {
		t.Errorf("multiplier source = %q, want the phrase that produced it", col.MultiplierSource)
	}
	if got := num(t, table, 2, 1); got != 3.863e9 {
		t.Errorf("December = %v, want 3,863,000,000", got)
	}
	// The raw cell survives the multiplication. A reviewer comparing the grid
	// against the page has to see what the page printed.
	if table.Rows[2].Cells[1].Raw != "3.863" {
		t.Errorf("raw cell = %q, want the document's own text", table.Rows[2].Cells[1].Raw)
	}

	// A scale word in the caption governs the whole table, not one column.
	caption := firstTable(t, page(1, [][]string{
		{"LAPORAN PENJUALAN (dalam ribuan)", "", ""},
		{"Bulan", "Unit", "Nilai"},
		{"Oktober", "120", "3.377"},
		{"November", "130", "3.708"},
		{"Desember", "140", "3.863"},
	}))
	for i, col := range caption.Columns[1:] {
		if col.Multiplier != 1e3 {
			t.Errorf("column %d multiplier = %v, want 1e3 from the caption", i+1, col.Multiplier)
		}
	}
}

// A cell that states its own scale is not multiplied twice. This is the one
// arithmetic mistake in this package that would be silent and enormous.
func TestACellWithItsOwnMagnitudeIsNotMultipliedAgain(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai (dalam jutaan)"},
		{"Oktober", "1,2 juta"},
		{"November", "3,4 juta"},
		{"Desember", "5,6 juta"},
	}))
	if got := num(t, table, 0, 1); got != 1.2e6 {
		t.Errorf("October = %v, want 1,200,000 — the cell said juta and the header said jutaan", got)
	}
}

// Acceptance: a title line above the table does not become a data row. T-P2's
// live gate produced exactly this — the text strategy split "LAPORAN PENJUALAN
// Q4 2024" across two cells of a four-column grid.
func TestTheTitleAboveATableIsNotARow(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"LAPORAN PENJUA", "LAN Q4 2024", "", ""},
		{"Bulan", "Transaksi", "Unit", "Nilai"},
		{"Oktober", "300", "120", "3.377.718.500"},
		{"November", "310", "130", "3.708.552.300"},
		{"Desember", "320", "140", "3.863.405.700"},
	}))

	if len(table.Rows) != 3 {
		t.Fatalf("got %d data rows, want 3 — the title became one", len(table.Rows))
	}
	if !strings.Contains(table.Title, "LAPORAN PENJUALAN") {
		t.Errorf("title = %q, want the caption reassembled across the split", table.Title)
	}
	if table.Columns[3].Header != "Nilai" {
		t.Errorf("column 3 header = %q, want Nilai", table.Columns[3].Header)
	}
	if got := num(t, table, 2, 3); got != 3863405700 {
		t.Errorf("December value = %v, want 3,863,405,700", got)
	}
	if len(table.Notes) == 0 || !strings.Contains(table.Notes[0], "caption") {
		t.Errorf("notes = %v, want the dropped caption written down", table.Notes)
	}
}

// Acceptance: a three-page table with a repeated header becomes one table with
// the pages' rows in order.
func TestAThreePageTableIsOneTable(t *testing.T) {
	header := []string{"Bulan", "Nilai"}
	tables := Build([]docparse.Page{
		page(1, [][]string{header, {"Januari", "100"}, {"Februari", "200"}}),
		page(2, [][]string{header, {"Maret", "300"}, {"April", "400"}}),
		page(3, [][]string{header, {"Mei", "500"}, {"Juni", "600"}}),
	}, Options{})

	if len(tables) != 1 {
		t.Fatalf("got %d tables, want 1 joined table", len(tables))
	}
	got := tables[0]
	if got.FirstPage != 1 || got.LastPage != 3 {
		t.Errorf("pages = %d–%d, want 1–3", got.FirstPage, got.LastPage)
	}
	want := []string{"Januari", "Februari", "Maret", "April", "Mei", "Juni"}
	if len(got.Rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got.Rows), len(want))
	}
	for i, month := range want {
		if got.Rows[i].Cells[0].Raw != month {
			t.Errorf("row %d = %q, want %q — the pages are out of order", i, got.Rows[i].Cells[0].Raw, month)
		}
		wantPage := i/2 + 1
		if got.Rows[i].Page != wantPage {
			t.Errorf("row %d says page %d, want %d — the provenance was lost in the join",
				i, got.Rows[i].Page, wantPage)
		}
	}
	if len(got.Boxes) != 3 {
		t.Errorf("got %d page rectangles, want 3 — the review surface needs one per page", len(got.Boxes))
	}
}

// The conservative half of the same rule: a different table that happens to
// follow is not joined.
func TestATableWithADifferentHeaderIsNotJoined(t *testing.T) {
	tables := Build([]docparse.Page{
		page(1, [][]string{{"Bulan", "Nilai"}, {"Januari", "100"}, {"Februari", "200"}}),
		page(2, [][]string{{"Produk", "Stok"}, {"Kopi", "300"}, {"Teh", "400"}}),
	}, Options{})
	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2 — two different tables were joined into one", len(tables))
	}
}

// Acceptance: a TOTAL row is flagged and excluded from the data rows.
func TestATotalRowIsHeldOutOfTheData(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377.718.500"},
		{"November", "3.708.552.300"},
		{"Desember", "3.863.405.700"},
		{"TOTAL", "10.949.676.500"},
	}))

	if len(table.Rows) != 3 {
		t.Fatalf("got %d data rows, want 3 — the TOTAL row is data", len(table.Rows))
	}
	if len(table.Totals) != 1 {
		t.Fatalf("got %d total rows, want 1 — a flagged total must be kept, not dropped", len(table.Totals))
	}
	if table.Totals[0].Total != "label" {
		t.Errorf("total row recognised as %q, want \"label\"", table.Totals[0].Total)
	}
	// Indonesian too, and that is not a courtesy: this deployment's documents
	// are mostly Indonesian, and an English-only check would miss most of them.
	id := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "100"},
		{"November", "200"},
		{"Jumlah", "300"},
	}))
	if len(id.Totals) != 1 {
		t.Errorf("\"Jumlah\" was not recognised as a total row")
	}
}

// Acceptance: a column with one unparseable cell types as text rather than
// dropping the cell. The dropped cell is the failure that matters — a figure
// that disappears from a source is a figure nobody can miss.
func TestOneUnparseableCellMakesTheWholeColumnText(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377"},
		{"November", "tidak tersedia"},
		{"Desember", "3.863"},
	}))

	if table.Columns[1].Type != ColumnText {
		t.Fatalf("column type = %s, want text", table.Columns[1].Type)
	}
	if len(table.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(table.Rows))
	}
	if table.Rows[1].Cells[1].Raw != "tidak tersedia" {
		t.Errorf("the unparseable cell was lost: %q", table.Rows[1].Cells[1].Raw)
	}
	if table.Rows[0].Cells[1].Num != nil {
		t.Error("a text column produced typed values")
	}
}

func TestColumnNamesAreSlugsAndUnique(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Q4 2024", "", "Q3 2024", ""},
		{"Actual", "Budget", "Actual", "Budget"},
		{"100", "110", "90", "95"},
		{"200", "210", "190", "195"},
	}))

	if got := table.Columns[0].Header; got != "Q4 2024"+headerJoin+"Actual" {
		t.Errorf("merged header = %q, want the two rows joined", got)
	}
	if got := table.Columns[1].Header; got != "Q4 2024"+headerJoin+"Budget" {
		t.Errorf("a merged header cell did not span to its neighbour: %q", got)
	}
	seen := map[string]bool{}
	for _, c := range table.Columns {
		if c.Name == "" {
			t.Fatal("a column has no name")
		}
		if seen[c.Name] {
			t.Fatalf("duplicate column name %q — CREATE TABLE would fail at publish", c.Name)
		}
		seen[c.Name] = true
		for _, r := range c.Name {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				t.Fatalf("column name %q holds %q, which is not slug-safe", c.Name, r)
			}
		}
	}
}

func TestDatesTypeAsDates(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Tanggal", "Nilai"},
		{"01/10/2024", "100"},
		{"15 November 2024", "200"},
		{"2024-12-31", "300"},
	}))
	if table.Columns[0].Type != ColumnDate {
		t.Fatalf("column type = %s, want date", table.Columns[0].Type)
	}
	for i, want := range []string{"2024-10-01", "2024-11-15", "2024-12-31"} {
		if got := table.Rows[i].Cells[0].Date; got != want {
			t.Errorf("row %d date = %q, want %q", i, got, want)
		}
	}
}

// Two prose lines are not a two-by-two grid. The text strategy will offer them
// as one, and a source built out of them is a source of sentences.
func TestAGridWithNoBodyIsNotATable(t *testing.T) {
	out := Build([]docparse.Page{page(1, [][]string{
		{"Catatan:", "seluruh angka telah diaudit"},
	})}, Options{MinRows: 2})
	if len(out) != 0 {
		t.Fatalf("got %d tables from two prose lines, want 0", len(out))
	}
}

func TestAScannedPageContributesNothing(t *testing.T) {
	scan := docparse.Page{Number: 2, Kind: docparse.KindNeedsOCR}
	out := Build([]docparse.Page{scan}, Options{})
	if len(out) != 0 {
		t.Fatalf("got %d tables off a page nothing read, want 0", len(out))
	}
}
