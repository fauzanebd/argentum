package pdf

import (
	"strings"
	"testing"

	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"

	"github.com/fauzanebd/argentum/internal/report/labels"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// T-R6. Three ways this renderer used to lose content without saying so. Each
// test asserts the disclosure, not the layout: a document may cut something it
// cannot fit, and may not do it quietly.

// widestLine is the width of the longest line s wraps to, which is what decides
// whether anything is drawn past the right margin. Measuring the whole string
// would fail every legitimately wrapped heading.
func widestLine(s string, family string, style fontstyle.Type, size, colWidth float64) float64 {
	widest := 0.0
	for _, ln := range wrapLines(s, family, style, size, colWidth) {
		if w := textWidth(ln, family, style, size); w > widest {
			widest = w
		}
	}
	return widest
}

// sku is the realistic version of "a heading with no spaces in it": line
// breaking happens at spaces and nowhere else, so gofpdf drew this straight
// past the right margin and off the sheet.
const sku = "SKU-JKT-2026-ELEKTRONIK-RUMAHTANGGA-KIPASANGIN-STANDINGFAN-16INCH-PUTIH-GARANSI2TAHUN"

func headingDoc(text string) *spec.Document {
	return &spec.Document{
		SpecVersion: 2,
		Format:      "pdf",
		Title:       text,
		Currency:    "IDR",
		Locale:      "id-ID",
		GeneratedAt: "2026-08-01T09:00:00Z",
		Content: spec.Content{Sections: []spec.Section{
			{Type: "cover", Text: text},
			{Type: "heading", Text: text, Level: 1},
			{Type: "paragraph", Text: "Isi laporan."},
			{Type: "heading", Text: text, Level: 2},
			{Type: "paragraph", Text: "Isi laporan."},
		}},
	}
}

// TestUnbrokenHeadingIsClippedNotDrawnOffThePage is the acceptance failure
// measure.go's doc comment already names, found on the four paths that never
// went through it: three of them measured the string's height and then handed
// the raw text to maroto, and the running header did not measure at all.
//
// The four are asserted together because they are one bug, and separately
// because each is drawn at its own size against its own measure — a string
// clipped to fit the header is still four times too wide for the cover.
func TestUnbrokenHeadingIsClippedNotDrawnOffThePage(t *testing.T) {
	pages := pageTexts(t, headingDoc(sku), Options{})
	if len(pages) < 2 {
		t.Fatalf("expected a cover and a body page, got %d", len(pages))
	}

	// Identified by length: every one of them is the same token cut to a
	// different width, so the order they appear in is the order of the sites.
	sites := []struct {
		name   string
		page   int
		family string
		style  fontstyle.Type
		size   float64
		width  float64
	}{
		{"cover title", 0, theme.FontDisplay, fontstyle.Bold, theme.TypeScale.Display, contentWidth()},
		{"running header", 1, theme.FontBody, fontstyle.Normal, theme.TypeScale.Caption, colWidth(theme.GridCols - 40)},
		{"h1", 1, theme.FontDisplay, fontstyle.Bold, theme.TypeScale.H1, contentWidth()},
		{"h2", 1, theme.FontMedium, fontstyle.Normal, theme.TypeScale.H2, contentWidth()},
	}

	seen := 0
	for _, site := range sites {
		for _, s := range pages[site.page] {
			if !strings.Contains(s, "SKU-JKT-2026") {
				continue
			}
			if w := widestLine(s, site.family, site.style, site.size, site.width); w > site.width {
				continue // drawn at one of the other sites' sizes
			}
			seen++
			if !strings.HasSuffix(s, "…") {
				t.Errorf("%s: cut with no ellipsis to show for it: %q", site.name, s)
			}
			if strings.Contains(s, "GARANSI2TAHUN") {
				t.Errorf("%s: the whole token survived, so nothing was clipped: %q", site.name, s)
			}
			break
		}
	}
	if seen != len(sites) {
		t.Errorf("only %d of %d title sites drew a string inside its own measure; "+
			"one of them is still running off the page", seen, len(sites))
	}
}

// TestSpacedHeadingStillWraps bounds the fix. Clipping is for the case wrapping
// cannot handle; a normal long heading must still get both of its lines.
func TestSpacedHeadingStillWraps(t *testing.T) {
	const long = "Ringkasan Kinerja Penjualan Kuartal Kedua Untuk Seluruh Wilayah Operasional Indonesia Bagian Barat Dan Timur"
	pages := pageTexts(t, headingDoc(long), Options{})

	var all []string
	for _, p := range pages {
		all = append(all, p...)
	}
	if !contains(all, "Ringkasan Kinerja Penjualan") {
		t.Fatal("the heading did not render")
	}
	// The tail is the half that a line-count truncation would have eaten.
	if !contains(all, "Bagian Barat Dan Timur") {
		t.Error("a heading that only needed wrapping was truncated instead")
	}
	for _, s := range all {
		if strings.Contains(s, "Ringkasan Kinerja") && strings.HasSuffix(s, "…") {
			t.Errorf("a wrappable heading was clipped: %q", s)
		}
	}
}

// wideTable builds a currency table with n numeric columns, which is how a
// table runs out of measure.
func wideTable(n int, currency, locale string) *spec.Document {
	cols := make([]spec.Column, n)
	cols[0] = spec.Column{Label: "Wilayah", Fmt: "text"}
	for i := 1; i < n; i++ {
		cols[i] = spec.Column{Label: "Bulan " + string(rune('A'+i)), Fmt: "currency", Align: "right"}
	}
	rows := make([][]spec.Cell, 4)
	for r := range rows {
		cells := make([]spec.Cell, n)
		cells[0] = spec.Cell{V: "Jawa Barat"}
		for c := 1; c < n; c++ {
			cells[c] = spec.Cell{V: float64((r + 1) * c * 918273)}
		}
		rows[r] = cells
	}
	return &spec.Document{
		SpecVersion: 2, Format: "pdf", Title: "Lebar", Currency: currency, Locale: locale,
		GeneratedAt: "2026-08-01T09:00:00Z",
		Content: spec.Content{Sections: []spec.Section{
			{Type: "table", Caption: "Angka per bulan", Columns: cols, Rows: rows},
		}},
	}
}

// TestTruncatedFiguresAreDisclosed is the table half of the same standard. A
// chart that drops series says so in its caption; a table that drops digits
// said nothing, and "$918,273.…" is indistinguishable from a value that
// legitimately ends there.
func TestTruncatedFiguresAreDisclosed(t *testing.T) {
	doc := wideTable(12, "IDR", "id-ID")
	var all []string
	for _, p := range pageTexts(t, doc, Options{}) {
		all = append(all, p...)
	}

	if !contains(all, "…") {
		t.Fatal("12 currency columns on A4 did not truncate; the fixture no longer tests anything")
	}
	note := labels.For("id").CellsTruncated
	if !contains(all, note) {
		t.Errorf("figures were cut with no disclosure in the caption; wanted %q", note)
	}
	// Appended to the model's caption, not in place of it.
	if !contains(all, "Angka per bulan") {
		t.Error("the disclosure replaced the caption instead of extending it")
	}
}

// TestDisclosureFollowsTheDocumentLocale keeps the note from being the one
// English sentence in an Indonesian report — the failure labels.Set exists to
// prevent.
func TestDisclosureFollowsTheDocumentLocale(t *testing.T) {
	var all []string
	for _, p := range pageTexts(t, wideTable(12, "USD", "en-US"), Options{}) {
		all = append(all, p...)
	}
	if !contains(all, labels.For("en").CellsTruncated) {
		t.Error("an en-US document did not get the English disclosure")
	}
	if contains(all, labels.For("id").CellsTruncated) {
		t.Error("an en-US document got the Indonesian disclosure")
	}
}

// TestWrappedTextCellIsNotDisclosed bounds the disclosure. A long description
// cut at the three-line cap is this renderer working as designed; captioning it
// would fire on ordinary tables, and a notice that fires on ordinary tables
// stops being read.
func TestWrappedTextCellIsNotDisclosed(t *testing.T) {
	doc := &spec.Document{
		SpecVersion: 2, Format: "pdf", Title: "Teks", Currency: "IDR", Locale: "id-ID",
		GeneratedAt: "2026-08-01T09:00:00Z",
		Content: spec.Content{Sections: []spec.Section{{
			Type:    "table",
			Caption: "Deskripsi panjang",
			Columns: []spec.Column{
				{Label: "Deskripsi", Fmt: "text"},
				{Label: "Nilai", Fmt: "currency", Align: "right"},
			},
			Rows: [][]spec.Cell{
				{{V: strings.Repeat("keterangan produk yang sangat panjang ", 12)}, {V: 918273.0}},
				{{V: "Singkat"}, {V: 4000.0}},
			},
		}}},
	}

	var all []string
	for _, p := range pageTexts(t, doc, Options{}) {
		all = append(all, p...)
	}
	if !contains(all, "…") {
		t.Fatal("the long text cell was not truncated; the fixture no longer tests anything")
	}
	if contains(all, labels.For("id").CellsTruncated) {
		t.Error("a text cell hitting the line cap raised the figures-were-cut disclosure")
	}
}

// TestLargeWholeCurrencyColumnDropsItsCents is format.ColumnDecimals seen from
// the document: the renderer is where the old behaviour was visible, so it is
// where the fix has to be visible too.
func TestLargeWholeCurrencyColumnDropsItsCents(t *testing.T) {
	doc := &spec.Document{
		SpecVersion: 2, Format: "pdf", Title: "Revenue", Currency: "USD", Locale: "en-US",
		GeneratedAt: "2026-08-01T09:00:00Z",
		Content: spec.Content{Sections: []spec.Section{{
			Type:    "table",
			Columns: []spec.Column{{Label: "Region", Fmt: "text"}, {Label: "Revenue", Fmt: "currency", Align: "right"}},
			Rows: [][]spec.Cell{
				{{V: "North"}, {V: 486000000.0}},
				{{V: "South"}, {V: 401000000.0}},
			},
			TotalRow: []spec.Cell{{V: "Total"}, {V: 887000000.0}},
		}}},
	}

	var all []string
	for _, p := range pageTexts(t, doc, Options{}) {
		all = append(all, p...)
	}
	if !contains(all, "$486,000,000") {
		t.Error("expected a cell reading $486,000,000")
	}
	if contains(all, "$486,000,000.00") {
		t.Error("a nine-figure whole-dollar column still carries a cents field")
	}
	// The total is formatted by the same column options, so it must agree.
	if contains(all, "$887,000,000.00") {
		t.Error("the total row disagrees with the column above it")
	}
}
