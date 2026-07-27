package document

import (
	"encoding/json"
	"testing"

	"github.com/fauzanebd/argentum/internal/report/spec"
)

// A v2 spec still has to produce a spreadsheet and a CSV, and those renderers
// take the v1 types. The conversion is where a typed cell becomes a string, and
// the rule it has to hold is that it becomes the string the *data* says, not
// the one the document would show: nobody sums a column of "Rp 1.234.567,00".
func TestFromReportSpecKeepsCellsRaw(t *testing.T) {
	raw := `{
	  "spec_version": 2,
	  "format": "xlsx",
	  "locale": "id",
	  "currency": "IDR",
	  "content": {
	    "table": {
	      "columns": [{"label":"Bulan"}, {"label":"Pendapatan","fmt":"currency"}, {"label":"Tumbuh","fmt":"percent"}],
	      "rows": [["Januari", 268431200, 3.1], ["Februari", 251902800, -6.2]],
	      "total_row": ["Total", 520334000, -1.6]
	    }
	  }
	}`
	var doc spec.Document
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc.Normalize()
	if err := doc.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	v1 := FromReportSpec(&doc)
	if v1.Content.Table == nil {
		t.Fatal("table did not survive the conversion")
	}
	got := v1.Content.Table.Rows
	want := [][]string{
		{"Januari", "268431200", "3.1"},
		{"Februari", "251902800", "-6.2"},
		{"Total", "520334000", "-1.6"}, // the total row is a row in a spreadsheet
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("row %d col %d: got %q, want %q", i, j, got[i][j], want[i][j])
			}
		}
	}

	// And the renderers still accept it.
	if _, err := RenderXLSX(v1); err != nil {
		t.Fatalf("render xlsx: %v", err)
	}
}

// The v1 JSON shape has to land in the same Go types as the v2 one — that is
// the whole of the compatibility story, and it lives in two custom
// unmarshalers rather than in a translation layer.
func TestV1JSONShapeUnmarshalsIntoTheSpec(t *testing.T) {
	raw := `{
	  "format": "pdf",
	  "content": {"sections": [
	    {"type": "table", "columns": ["Item", "Qty"], "rows": [["Widget", "3"]]}
	  ]}
	}`
	var doc spec.Document
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sec := doc.Content.Sections[0]
	if len(sec.Columns) != 2 || sec.Columns[0].Label != "Item" {
		t.Fatalf("bare string columns did not become Column values: %#v", sec.Columns)
	}
	if sec.Rows[0][0].V != "Widget" {
		t.Fatalf("bare string cells did not become Cell values: %#v", sec.Rows[0])
	}
	if doc.V2() {
		t.Error("a spec with no spec_version must not opt into the v2 layout")
	}
}

// A long numeric id has to survive the round trip. json.Unmarshal into `any`
// would make this a float64 and print it as 1.2345678901234568e+19.
func TestLargeIntegerCellDoesNotLosePrecision(t *testing.T) {
	var c spec.Cell
	if err := json.Unmarshal([]byte(`12345678901234567890`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := rawCell(c); got != "12345678901234567890" {
		t.Errorf("got %q, want the digits unchanged", got)
	}
}

// ToReportSpec is the other direction: the v1 Go types the tool still uses for
// XLSX and CSV, rendered as a PDF through the new renderer.
func TestToReportSpecRendersThroughTheNewRenderer(t *testing.T) {
	v1 := &Spec{
		Format: "pdf",
		Title:  "Round trip",
		Content: Content{Sections: []Section{
			{Type: "key_value", Items: []KV{{K: "Date", V: "2026-05-09"}}},
			{Type: "table", Columns: []string{"Item", "Price"}, Rows: [][]string{{"Widget", "$9.99"}}},
		}},
	}
	out, err := RenderPDF(v1)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out) < 200 {
		t.Fatalf("pdf too small: %d bytes", len(out))
	}
}
