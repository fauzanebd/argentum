package pdf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnfercher/go-tree/node"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The four fixtures are the ones T-R2's gate names: a monthly sales report, an
// invoice, a KPI summary and a 200-row export, plus a v1 spec to prove the
// legacy shape still renders. They are JSON rather than Go literals because
// JSON is what the model sends — a Go fixture would test the renderer without
// testing the two custom unmarshalers that let a v1 payload and a v2 payload
// land in the same type.
var fixtures = []string{
	"monthly_sales.json",
	"invoice.json",
	"kpi_summary.json",
	"export_200.json",
	"v1_legacy.json",
}

func loadFixture(t *testing.T, name string) *spec.Document {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc spec.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	doc.Normalize()
	if err := doc.Validate(); err != nil {
		t.Fatalf("validate %s: %v", name, err)
	}
	return &doc
}

func renderFixture(t *testing.T, name string) []byte {
	t.Helper()
	out, err := Render(loadFixture(t, name), Options{})
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out
}

// TestFixturesRender is the smoke gate: every fixture produces a PDF that
// pdfcpu is willing to parse, made of the embedded faces and not of whatever
// the reader happens to have installed.
func TestFixturesRender(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			out := renderFixture(t, name)

			if !bytes.HasPrefix(out, []byte("%PDF-")) {
				t.Fatalf("not a PDF: %q", out[:min(8, len(out))])
			}
			if !bytes.Contains(out, []byte(theme.FontBody)) {
				t.Errorf("PDF does not name the %q family", theme.FontBody)
			}
			if !bytes.Contains(out, []byte("/FontFile2")) {
				t.Error("PDF references fonts without embedding them")
			}
			// The whole point of T-R1's embedded faces: a document that falls
			// back to a core font renders differently on every machine.
			if bytes.Contains(out, []byte("Helvetica")) {
				t.Error("PDF names Helvetica: the theme fonts did not take effect")
			}

			pages, err := api.PageCount(bytes.NewReader(out), model.NewDefaultConfiguration())
			if err != nil {
				t.Fatalf("pdfcpu page count: %v", err)
			}
			t.Logf("%s: %d pages, %d bytes, sha256 %s", name, pages, len(out), sum(out))
		})
	}
}

// TestFixturesValidate is the acceptance item "pdfcpu validate passes on every
// fixture", run in-process rather than as a shell step so it gates CI without
// anyone having to install the binary.
func TestFixturesValidate(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			out := renderFixture(t, name)
			// The default configuration is relaxed validation, which is what
			// the `pdfcpu validate` command does. Strict mode rejects every
			// PDF gofpdf has ever produced with an embedded UTF-8 font: it
			// requires /FontFamily in the font descriptor and gofpdf writes
			// none, for CID fonts where the spec marks the entry optional.
			// That is a library gap this ticket did not introduce and cannot
			// close without forking the writer.
			if err := api.Validate(bytes.NewReader(out), model.NewDefaultConfiguration()); err != nil {
				t.Fatalf("pdfcpu validate: %v", err)
			}
		})
	}
}

// TestDeterministicBytes is what makes a golden test possible at all: with a
// fixed generated_at, two renders of the same spec are byte-identical. gofpdf
// writes the creation date into the trailer, so a renderer that read the clock
// would produce a different file every second.
func TestDeterministicBytes(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			doc := loadFixture(t, name)
			if strings.TrimSpace(doc.GeneratedAt) == "" {
				t.Skip("fixture has no generated_at; bytes are not reproducible by design")
			}
			first, err := Render(doc, Options{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			second, err := Render(loadFixture(t, name), Options{})
			if err != nil {
				t.Fatalf("re-render: %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("bytes differ between runs: %s vs %s", sum(first), sum(second))
			}

			// Comparing two renders only catches a wall-clock timestamp when
			// the pair happens to straddle a second, which is how /ModDate
			// survived six local runs and failed twice in CI. Both timestamps
			// are asserted directly instead: gofpdf writes them as
			// D:YYYYMMDDhhmmss, and both must be the spec's generated_at.
			when, _ := doc.Generated()
			stamp := when.UTC().Format("20060102150405")
			for _, key := range []string{"/CreationDate", "/ModDate"} {
				want := []byte(key + " (D:" + stamp + ")")
				if !bytes.Contains(first, want) {
					t.Errorf("%s is not pinned to generated_at; want %q", key, want)
				}
			}
			t.Logf("%s: %d bytes, sha256 %s (identical across two runs)", name, len(first), sum(first))
		})
	}
}

// pageTexts walks maroto's component tree and returns every text value, page
// by page.
//
// This is the only way to assert on layout: the rendered bytes encode text as
// subset glyph ids, so nothing downstream — not grep, not pdfcpu — can read
// "Prepared for" back out of a finished document.
func pageTexts(t *testing.T, doc *spec.Document, opts Options) [][]string {
	t.Helper()
	r, err := newRenderer(doc, opts)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	if err := r.build(); err != nil {
		t.Fatalf("build: %v", err)
	}
	tree := r.m.GetStructure()

	var pages [][]string
	for _, pageNode := range tree.GetNexts() {
		var texts []string
		collectText(pageNode, &texts)
		pages = append(pages, texts)
	}
	return pages
}

func collectText(n *node.Node[core.Structure], out *[]string) {
	data := n.GetData()
	if value, ok := data.Value.(string); ok && data.Type == "text" && strings.TrimSpace(value) != "" {
		*out = append(*out, value)
	}
	for _, next := range n.GetNexts() {
		collectText(next, out)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func count(haystack []string, needle string) int {
	n := 0
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			n++
		}
	}
	return n
}

// TestCoverIsCleanAndHeaderRunsFromPageTwo is the layout rule that drove the
// whole render order: maroto adds registered header rows to the current page,
// so the cover has to be drawn and closed before the header exists.
func TestCoverIsCleanAndHeaderRunsFromPageTwo(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	pages := pageTexts(t, doc, Options{})

	if len(pages) < 3 {
		t.Fatalf("expected a multi-page document, got %d pages", len(pages))
	}

	cover := pages[0]
	if !contains(cover, "Laporan Penjualan Bulanan") {
		t.Error("cover is missing the document title")
	}
	// The cover's own labels follow the document's locale, not the process's.
	if !contains(cover, "Disiapkan untuk") || !contains(cover, "Dewan Direksi") {
		t.Error("cover is missing the prepared-for block")
	}
	if !contains(cover, "RAHASIA — INTERNAL") {
		t.Error("cover is missing the confidentiality label")
	}
	// The running header's identity mark and the footer's generated stamp are
	// the two things that must not appear on page 1.
	if count(cover, "Dibuat 27 Juli 2026 09:30") > 1 {
		t.Error("the running footer was drawn on the cover")
	}
	if count(cover, "Argentum") > 1 {
		t.Error("the running header was drawn on the cover")
	}

	for i, p := range pages[1:] {
		if !contains(p, "Argentum") {
			t.Errorf("page %d has no running header", i+2)
		}
		if !contains(p, "Dibuat 27 Juli 2026 09:30") {
			t.Errorf("page %d has no running footer", i+2)
		}
		if !contains(p, "Rahasia — Internal") {
			t.Errorf("page %d footer has no confidentiality label", i+2)
		}
	}
}

// TestTableHeaderRepeatsWithoutOrphans is the 200-row acceptance item: the
// header row appears on every page the table spans, and never as the last row
// on a page.
func TestTableHeaderRepeatsWithoutOrphans(t *testing.T) {
	doc := loadFixture(t, "export_200.json")
	pages := pageTexts(t, doc, Options{})

	if len(pages) < 4 {
		t.Fatalf("200 rows should span several pages, got %d", len(pages))
	}

	// "No. Pesanan" is a header label and appears in no data cell.
	tablePages := 0
	for i, p := range pages {
		headers := count(p, "No. Pesanan")
		if headers == 0 {
			continue
		}
		tablePages++
		if headers > 1 {
			t.Errorf("page %d repeats the table header %d times", i+1, headers)
		}
		// An orphan is a header row with nothing under it. Every page that
		// carries the header must also carry at least one data row, and every
		// data row has two rupiah cells in it.
		if !contains(p, "Rp ") {
			t.Errorf("page %d has an orphaned table header", i+1)
		}
	}
	if tablePages < 3 {
		t.Fatalf("table header repeated on only %d pages", tablePages)
	}
	t.Logf("200-row export: %d pages, header repeated on %d of them", len(pages), tablePages)
}

// TestLocaleFormattingInCells proves the renderer, not the model, decides how
// a number reads: the fixture passes 3863405700 and 12.4, and the document
// says "Rp 3.863.405.700" and "12,4%".
func TestLocaleFormattingInCells(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	var all []string
	for _, p := range pageTexts(t, doc, Options{}) {
		all = append(all, p...)
	}

	for _, want := range []string{
		"Rp 3.863.405.700", // id grouping, no decimals on rupiah
		"Rp 268.431.200",
		"12,4%",  // id decimal comma
		"-13,3%", // negative growth keeps its sign
		"48.219", // a count is grouped but not currency
	} {
		if !contains(all, want) {
			t.Errorf("expected a cell reading %q", want)
		}
	}
	// The KPI card compacts; the table does not.
	if !contains(all, "Rp 3,86 Miliar") {
		t.Error("KPI card did not compact the headline figure")
	}
	// And the model's own pre-formatted strings must not survive as-is where a
	// numeric column was declared.
	if contains(all, "3863405700") {
		t.Error("a raw figure reached the page unformatted")
	}
}

// TestEnglishLocale is the other half: the same code path with en/USD.
func TestEnglishLocale(t *testing.T) {
	doc := loadFixture(t, "invoice.json")
	var all []string
	for _, p := range pageTexts(t, doc, Options{}) {
		all = append(all, p...)
	}
	for _, want := range []string{"$2,400.00", "$5,600.00", "Generated 27 July 2026"} {
		if !contains(all, want) {
			t.Errorf("expected a cell reading %q", want)
		}
	}
}

// TestV1SpecGetsNoChrome is the compatibility contract stated as a test: a
// spec with no spec_version renders the old flat document — no cover, no
// running header, no numbered headings — on the new renderer.
func TestV1SpecGetsNoChrome(t *testing.T) {
	doc := loadFixture(t, "v1_legacy.json")
	pages := pageTexts(t, doc, Options{})
	if len(pages) != 1 {
		t.Fatalf("expected one page, got %d", len(pages))
	}
	p := pages[0]
	if !contains(p, "Line Items") {
		t.Error("v1 heading did not render")
	}
	if contains(p, "1. Line Items") {
		t.Error("v1 headings must not be numbered")
	}
	if contains(p, "Generated ") {
		t.Error("v1 must not get a running footer")
	}
	if !contains(p, "$9.99") {
		t.Error("v1 string cells must render exactly as given")
	}
}

// TestColumnWidthsAreWeighted is the fix for the eight-column table that used
// to be unreadable: the description column has to be materially wider than
// the quantity column, and every row still has to sum to the grid.
func TestColumnWidthsAreWeighted(t *testing.T) {
	doc := loadFixture(t, "invoice.json")
	r, err := newRenderer(doc, Options{})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	var table *spec.Table
	for _, sec := range doc.Content.Sections {
		if sec.Type == spec.SectionTable {
			table = sec.Table()
		}
	}
	if table == nil {
		t.Fatal("fixture has no table")
	}

	cols := r.resolveColumns(table)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.label
	}
	body := r.formatRows(table.Rows, cols)
	r.assignWidths(cols, header, body, r.formatRow(table.TotalRow, cols))

	sum := 0
	for _, c := range cols {
		sum += c.units
		if c.units < minColUnits {
			t.Errorf("column %q got %d units, below the %d minimum", c.label, c.units, minColUnits)
		}
	}
	if sum != theme.GridCols {
		t.Errorf("columns sum to %d units, want %d — maroto renders a short row as a gap", sum, theme.GridCols)
	}
	if cols[0].units <= cols[2].units*2 {
		t.Errorf("description column (%d units) is not materially wider than Qty (%d units)",
			cols[0].units, cols[2].units)
	}
	widths := make([]string, len(cols))
	for i, c := range cols {
		widths[i] = fmt.Sprintf("%s=%d", c.label, c.units)
	}
	t.Logf("invoice column units: %s", strings.Join(widths, " "))
}

// TestNumericColumnsAreRightAligned covers the typed-cell acceptance item from
// the layout side.
func TestNumericColumnsAreRightAligned(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	r, err := newRenderer(doc, Options{})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	for _, sec := range doc.Content.Sections {
		if sec.Type != spec.SectionTable {
			continue
		}
		cols := r.resolveColumns(sec.Table())
		for _, c := range cols {
			want := "L"
			if c.kind.Numeric() {
				want = "R"
			}
			if string(c.align) != want {
				t.Errorf("column %q (%s) aligned %q, want %q", c.label, c.kind, c.align, want)
			}
		}
	}
}

// TestChartSectionIsRejected pins the T-R3 boundary: a chart is refused with
// an instruction rather than dropped, because a report whose narrative refers
// to a figure that silently did not render is worse than an error.
func TestChartSectionIsRejected(t *testing.T) {
	doc := &spec.Document{
		SpecVersion: 2,
		Format:      "pdf",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionChart, Chart: &spec.Chart{Type: "line"}},
		}},
	}
	err := doc.Validate()
	if err == nil {
		t.Fatal("expected chart sections to be rejected")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error should tell the model what to do instead, got %q", err)
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestWriteFixtureArtifacts writes the rendered fixtures out for a human to
// look at. Nothing in this file can tell you a document is ugly, and the
// acceptance gate for this ticket is a page you can see.
//
//	ARGENTUM_PDF_OUT=/tmp/pdf go test ./internal/report/pdf/ -run WriteFixture
func TestWriteFixtureArtifacts(t *testing.T) {
	dir := os.Getenv("ARGENTUM_PDF_OUT")
	if dir == "" {
		t.Skip("set ARGENTUM_PDF_OUT to write the rendered fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range fixtures {
		out := renderFixture(t, name)
		path := filepath.Join(dir, strings.TrimSuffix(name, ".json")+".pdf")
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(out))
	}
}
