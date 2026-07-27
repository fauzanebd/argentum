package document

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

// The PDF renderer that used to live here — a bold title, a bold row per
// heading, and a table whose columns were the 12-unit grid divided evenly —
// was replaced by internal/report/pdf in T-R2. What is left is the two
// conversions between this package's v1 types and the spec the renderers read.
//
// v1 is not a separate rendering path: spec.Column and spec.Cell unmarshal
// from both the v1 and v2 JSON shapes, so a v1 document is a v2 document whose
// cells are all text. What v1 does *not* get is the chrome — no cover, no
// running header, no numbered headings — because a spec that has been
// producing a plain document for three months should not start producing a
// different one because the backend was upgraded. Opting in is one field:
// spec_version: 2.

// RenderPDF renders a v1 spec.
func RenderPDF(s *Spec) ([]byte, error) {
	return pdf.Render(ToReportSpec(s), pdf.Options{})
}

// ToReportSpec converts the v1 tool types into the renderer's spec. Every cell
// becomes a text cell, which is exactly what v1 meant: the model had already
// formatted everything itself.
func ToReportSpec(s *Spec) *spec.Document {
	if s == nil {
		return nil
	}
	doc := &spec.Document{
		Format:   s.Format,
		Filename: s.Filename,
		Title:    s.Title,
	}
	if t := s.Content.Table; t != nil {
		doc.Content.Table = &spec.Table{
			Columns: toColumns(t.Columns),
			Rows:    toRows(t.Rows),
		}
	}
	for _, sec := range s.Content.Sections {
		out := spec.Section{
			Type:    sec.Type,
			Text:    sec.Text,
			Columns: toColumns(sec.Columns),
			Rows:    toRows(sec.Rows),
			Size:    sec.Size,
		}
		for _, kv := range sec.Items {
			out.Items = append(out.Items, spec.Item{K: kv.K, V: kv.V})
		}
		doc.Content.Sections = append(doc.Content.Sections, out)
	}
	for _, sh := range s.Content.Sheets {
		doc.Content.Sheets = append(doc.Content.Sheets, spec.Sheet{
			Name:    sh.Name,
			Columns: toColumns(sh.Columns),
			Rows:    toRows(sh.Rows),
		})
	}
	return doc
}

// FromReportSpec flattens a spec back into the v1 types the XLSX and CSV
// renderers take.
//
// Cells are stringified raw — no thousands separators, no currency symbols.
// A spreadsheet and a CSV are read by machines at least as often as by people,
// and "1.234.567,89" in a cell someone wants to sum is worse than useless.
// The PDF is where the formatting belongs, and that path does not come
// through here.
func FromReportSpec(doc *spec.Document) *Spec {
	if doc == nil {
		return nil
	}
	out := &Spec{
		Format:   doc.Format,
		Filename: doc.Filename,
		Title:    doc.Title,
	}
	if t := doc.Content.Table; t != nil {
		out.Content.Table = &Table{
			Columns: fromColumns(t.Columns),
			Rows:    fromRows(t.Rows, t.TotalRow),
		}
	}
	for _, sh := range doc.Content.Sheets {
		out.Content.Sheets = append(out.Content.Sheets, Sheet{
			Name:    sh.Name,
			Columns: fromColumns(sh.Columns),
			Rows:    fromRows(sh.Rows, nil),
		})
	}
	for _, sec := range doc.Content.Sections {
		v1 := Section{
			Type:    sec.Type,
			Text:    sec.Text,
			Columns: fromColumns(sec.Columns),
			Rows:    fromRows(sec.Rows, sec.TotalRow),
			Size:    sec.Size,
		}
		for _, item := range sec.Items {
			v1.Items = append(v1.Items, KV{K: item.KeyText(), V: rawCell(item.ValueCell())})
		}
		out.Content.Sections = append(out.Content.Sections, v1)
	}
	return out
}

func toColumns(labels []string) []spec.Column {
	if len(labels) == 0 {
		return nil
	}
	out := make([]spec.Column, len(labels))
	for i, l := range labels {
		out[i] = spec.Column{Label: l}
	}
	return out
}

func toRows(rows [][]string) [][]spec.Cell {
	if len(rows) == 0 {
		return nil
	}
	out := make([][]spec.Cell, len(rows))
	for i, row := range rows {
		cells := make([]spec.Cell, len(row))
		for j, v := range row {
			cells[j] = spec.Cell{V: v}
		}
		out[i] = cells
	}
	return out
}

func fromColumns(cols []spec.Column) []string {
	if len(cols) == 0 {
		return nil
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Label
	}
	return out
}

func fromRows(rows [][]spec.Cell, total []spec.Cell) [][]string {
	out := make([][]string, 0, len(rows)+1)
	for _, row := range rows {
		cells := make([]string, len(row))
		for j, c := range row {
			cells[j] = rawCell(c)
		}
		out = append(out, cells)
	}
	if len(total) > 0 {
		cells := make([]string, len(total))
		for j, c := range total {
			cells[j] = rawCell(c)
		}
		out = append(out, cells)
	}
	return out
}

// rawCell stringifies a cell without formatting it.
func rawCell(c spec.Cell) string {
	switch v := c.V.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
