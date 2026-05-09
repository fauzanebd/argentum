package document

import (
	"bytes"
	"testing"
)

func TestRenderCSV(t *testing.T) {
	spec := &Spec{
		Format: "csv",
		Content: Content{
			Table: &Table{
				Columns: []string{"name", "qty", "price"},
				Rows: [][]string{
					{"Widget", "3", "9.99"},
					{"Gadget, big", "1", "199.00"}, // comma should be quoted
				},
			},
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	out, err := RenderCSV(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := string(out)
	if got == "" {
		t.Fatal("empty output")
	}
	if !bytes.HasPrefix(out, []byte("name,qty,price\n")) {
		t.Fatalf("missing header: %q", got)
	}
	if !bytes.Contains(out, []byte(`"Gadget, big"`)) {
		t.Fatalf("comma not quoted: %q", got)
	}
}

func TestRenderXLSX_singleTable(t *testing.T) {
	spec := &Spec{
		Format: "xlsx",
		Title:  "My Sheet",
		Content: Content{
			Table: &Table{
				Columns: []string{"a", "b"},
				Rows:    [][]string{{"1", "2"}, {"3", "4"}},
			},
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	out, err := RenderXLSX(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out) < 100 {
		t.Fatalf("xlsx too small: %d bytes", len(out))
	}
	// XLSX is a zip — magic bytes "PK\x03\x04".
	if !bytes.HasPrefix(out, []byte{'P', 'K', 0x03, 0x04}) {
		t.Fatalf("bad xlsx magic bytes: %x", out[:4])
	}
}

func TestRenderXLSX_multiSheet(t *testing.T) {
	spec := &Spec{
		Format: "xlsx",
		Content: Content{
			Sheets: []Sheet{
				{Name: "Summary", Columns: []string{"k", "v"}, Rows: [][]string{{"total", "10"}}},
				{Name: "Detail", Columns: []string{"id", "amount"}, Rows: [][]string{{"1", "5"}, {"2", "5"}}},
			},
		},
	}
	out, err := RenderXLSX(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte{'P', 'K', 0x03, 0x04}) {
		t.Fatalf("bad magic bytes")
	}
}

func TestRenderPDF_sections(t *testing.T) {
	spec := &Spec{
		Format: "pdf",
		Title:  "Invoice #1234",
		Content: Content{
			Sections: []Section{
				{Type: "key_value", Items: []KV{{K: "Date", V: "2026-05-09"}, {K: "Customer", V: "Foo Inc."}}},
				{Type: "spacer", Size: 4},
				{Type: "heading", Text: "Line Items"},
				{Type: "table",
					Columns: []string{"Item", "Qty", "Price"},
					Rows:    [][]string{{"Widget", "3", "$9.99"}, {"Gadget", "1", "$199.00"}},
				},
				{Type: "paragraph", Text: "Thank you for your business."},
			},
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	out, err := RenderPDF(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out) < 200 {
		t.Fatalf("pdf too small: %d bytes", len(out))
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("bad pdf magic: %q", out[:8])
	}
}

func TestSpecValidate(t *testing.T) {
	cases := []struct {
		name    string
		spec    Spec
		wantErr bool
	}{
		{"bad format", Spec{Format: "docx"}, true},
		{"csv no table", Spec{Format: "csv"}, true},
		{"csv no columns", Spec{Format: "csv", Content: Content{Table: &Table{}}}, true},
		{"xlsx neither", Spec{Format: "xlsx"}, true},
		{"pdf neither", Spec{Format: "pdf"}, true},
		{"csv ok", Spec{Format: "csv", Content: Content{Table: &Table{Columns: []string{"a"}}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.spec.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}
