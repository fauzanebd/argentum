// Package document renders structured input specs into PDF / XLSX / CSV
// bytes. The shape is generic: the LLM populates a Spec, picks a format,
// and the renderer dispatches. The package name is "document" because
// the artifacts are not just reports — invoices, agreements, T&Cs,
// research summaries, exports all flow through here. Adding charts later
// means extending Section/Sheet types without touching the tool contract.
package document

import "fmt"

// Spec is the full input the generate_document tool accepts.
type Spec struct {
	Format   string  `json:"format"`
	Filename string  `json:"filename,omitempty"`
	Title    string  `json:"title,omitempty"`
	Content  Content `json:"content"`
}

// Content carries one of three shapes. CSV always uses Table.
// XLSX uses Sheets when present, otherwise Table (single-sheet).
// PDF uses Sections when present, otherwise Title + Table.
type Content struct {
	Table    *Table    `json:"table,omitempty"`
	Sections []Section `json:"sections,omitempty"`
	Sheets   []Sheet   `json:"sheets,omitempty"`
}

type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

type Sheet struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// Section is a building block for PDF reports.
//
//	type=heading      → Text rendered as bold heading
//	type=paragraph    → Text rendered as wrapped paragraph
//	type=key_value    → Items rendered as label/value pairs (invoices)
//	type=table        → Columns/Rows rendered as bordered table
//	type=spacer       → vertical gap (Size in mm)
type Section struct {
	Type    string     `json:"type"`
	Text    string     `json:"text,omitempty"`
	Items   []KV       `json:"items,omitempty"`
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	Size    float64    `json:"size,omitempty"`
}

type KV struct {
	K string `json:"k"`
	V string `json:"v"`
}

// Validate gives early, actionable errors before we hit a renderer.
func (s *Spec) Validate() error {
	switch s.Format {
	case "pdf", "xlsx", "csv":
	default:
		return fmt.Errorf("format must be one of pdf|xlsx|csv (got %q)", s.Format)
	}
	switch s.Format {
	case "csv":
		if s.Content.Table == nil {
			return fmt.Errorf("csv requires content.table")
		}
		if len(s.Content.Table.Columns) == 0 {
			return fmt.Errorf("csv requires content.table.columns")
		}
	case "xlsx":
		if s.Content.Table == nil && len(s.Content.Sheets) == 0 {
			return fmt.Errorf("xlsx requires content.table or content.sheets")
		}
	case "pdf":
		if len(s.Content.Sections) == 0 && s.Content.Table == nil {
			return fmt.Errorf("pdf requires content.sections or content.table")
		}
	}
	return nil
}
