// Package spec is the document description the renderers read.
//
// It is version 2 of the shape `generate_document` has accepted since
// 42391d2. Version 1 was a list of {heading, paragraph, key_value, table,
// spacer} sections whose cells were all strings, which put every formatting
// decision — thousands separators, decimal places, currency symbols, column
// widths — inside the model's head, one turn at a time. The results were
// inconsistent because the model is inconsistent.
//
// Version 2 moves those decisions to the renderer. A cell declares what it is
// ({v, fmt}) and the renderer decides how it looks. A column declares its
// weight and the renderer decides how wide it is. The model goes back to
// choosing content, which is the part it is good at.
//
// v1 is not deprecated and is not shimmed at the JSON layer: Column and Cell
// both unmarshal from either shape, so a v1 spec is a v2 spec whose columns
// are all text. `spec_version` only decides whether the *renderer* offers the
// v2 chrome (cover, running header, footer), because a document that has been
// rendering as a bare table for three months should not sprout a cover page
// because the backend was upgraded.
package spec

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Section type names. These are the `type` field values the model writes.
const (
	SectionCover     = "cover"
	SectionHeading   = "heading"
	SectionParagraph = "paragraph"
	SectionKPIRow    = "kpi_row"
	SectionTable     = "table"
	SectionCallout   = "callout"
	SectionKeyValue  = "key_value"
	SectionChart     = "chart"
	SectionFootnote  = "footnote"
	SectionPageBreak = "page_break"

	// SectionSpacer is v1 only. v2 spacing comes from the theme's vertical
	// rhythm, so a v2 spec that asks for one gets it, but nothing in the tool
	// description invites it any more.
	SectionSpacer = "spacer"
)

// Callout tones.
const (
	ToneInfo = "info"
	ToneWarn = "warn"
	ToneGood = "good"
)

// Document is the whole input: one file, one format, one content tree.
type Document struct {
	// SpecVersion is 2 for the enterprise layout, absent or 1 for the
	// original flat rendering.
	SpecVersion int `json:"spec_version,omitempty"`

	Format   string `json:"format"`
	Filename string `json:"filename,omitempty"`
	Title    string `json:"title,omitempty"`

	// Locale is "id" or "en" and decides separators, month names and
	// magnitude words. Empty means "derive from Currency".
	Locale string `json:"locale,omitempty"`

	// Currency is an ISO 4217 code. Empty means the caller's company default.
	Currency string `json:"currency,omitempty"`

	// GeneratedAt is an RFC3339 timestamp. It is a spec field rather than a
	// clock read so a golden test can render the same bytes twice; when the
	// model omits it the renderer stamps now.
	GeneratedAt string `json:"generated_at,omitempty"`

	Meta    Meta    `json:"meta,omitzero"`
	Content Content `json:"content"`
}

// Meta is what lands in the PDF's info dictionary. A document that leaves the
// building with an empty Title in its properties is one a records system
// cannot file.
type Meta struct {
	Author   string `json:"author,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Keywords string `json:"keywords,omitempty"`
}

// Content carries one of three shapes, unchanged from v1: PDF uses Sections
// (or a bare Table), CSV always uses Table, XLSX uses Sheets or Table.
type Content struct {
	Sections []Section `json:"sections,omitempty"`
	Table    *Table    `json:"table,omitempty"`
	Sheets   []Sheet   `json:"sheets,omitempty"`
}

// Section is one block in the document flow. Every payload field for every
// section type lives on this one struct rather than in a union, for two
// reasons: the tool's JSON schema is flat, and a model that puts `text` on a
// callout instead of inside it still renders something.
type Section struct {
	Type string `json:"type"`

	// cover
	Subtitle        string `json:"subtitle,omitempty"`
	Period          string `json:"period,omitempty"`
	PreparedFor     string `json:"prepared_for,omitempty"`
	PreparedBy      string `json:"prepared_by,omitempty"`
	Confidentiality string `json:"confidentiality,omitempty"`

	// heading / paragraph / footnote / callout
	Text  string `json:"text,omitempty"`
	Level int    `json:"level,omitempty"`
	Title string `json:"title,omitempty"`
	Tone  string `json:"tone,omitempty"`

	// key_value and kpi_row both carry `items`; Item holds both vocabularies.
	Items []Item `json:"items,omitempty"`

	// table
	Columns  []Column `json:"columns,omitempty"`
	Rows     [][]Cell `json:"rows,omitempty"`
	TotalRow []Cell   `json:"total_row,omitempty"`
	Caption  string   `json:"caption,omitempty"`

	// spacer (v1)
	Size float64 `json:"size,omitempty"`

	// chart — payload defined by T-R3.
	Chart *Chart `json:"chart,omitempty"`
}

// Item is a row of a key_value block or a card in a kpi_row.
//
// One struct for both because the model mixes the two vocabularies. A KPI card
// written as {k, v} still gets a card; a key/value row written as
// {label, value} still gets a row. Silently rendering nothing because the
// model picked the wrong pair of field names is not a defensible failure.
type Item struct {
	// key_value
	K string `json:"k,omitempty"`
	V string `json:"v,omitempty"`

	// kpi_row
	Label string `json:"label,omitempty"`
	Value *Cell  `json:"value,omitempty"`

	// DeltaPct is a period-over-period change in percent units: 12.5 is
	// +12.5%, not +1250%.
	DeltaPct *float64 `json:"delta_pct,omitempty"`

	// Direction is "up" or "down" when the caller wants to state it; empty
	// means read it off the sign of DeltaPct.
	Direction string `json:"direction,omitempty"`

	// HigherIsBetter decides whether an up arrow is green or red. Churn going
	// up is not good news, and a renderer that colours every rise green is
	// telling the reader something false. Defaults to true when absent.
	HigherIsBetter *bool `json:"higher_is_better,omitempty"`

	Fmt string `json:"fmt,omitempty"`
}

// KeyText is the label under either vocabulary.
func (i Item) KeyText() string {
	if i.Label != "" {
		return i.Label
	}
	return i.K
}

// ValueCell is the value under either vocabulary.
func (i Item) ValueCell() Cell {
	if i.Value != nil {
		c := *i.Value
		if c.Fmt == "" {
			c.Fmt = i.Fmt
		}
		return c
	}
	return Cell{V: i.V, Fmt: i.Fmt}
}

// GoodDirection reports whether the delta should read as good news.
func (i Item) GoodDirection() bool {
	up := i.Rising()
	better := i.HigherIsBetter == nil || *i.HigherIsBetter
	return up == better
}

// Rising reports whether the delta points up, preferring an explicit
// direction over the sign.
func (i Item) Rising() bool {
	switch strings.ToLower(strings.TrimSpace(i.Direction)) {
	case "up", "increase", "rising", "naik":
		return true
	case "down", "decrease", "falling", "turun":
		return false
	}
	return i.DeltaPct != nil && *i.DeltaPct >= 0
}

// Table is a standalone table — the top-level content.table, and what a table
// section flattens to.
type Table struct {
	Columns  []Column `json:"columns"`
	Rows     [][]Cell `json:"rows"`
	TotalRow []Cell   `json:"total_row,omitempty"`
	Caption  string   `json:"caption,omitempty"`
}

// Sheet is one XLSX tab. Unchanged from v1 apart from the cell type.
type Sheet struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
	Rows    [][]Cell `json:"rows"`
}

// Chart is the payload T-R3 renders. Declared here so the spec type does not
// change when the renderer arrives; until then Validate rejects the section
// rather than dropping it silently.
type Chart struct {
	Type   string    `json:"type,omitempty"` // line|bar|grouped_bar|stacked_bar|pie|donut|sparkline
	Title  string    `json:"title,omitempty"`
	Labels []string  `json:"labels,omitempty"`
	Series []Series  `json:"series,omitempty"`
	Fmt    string    `json:"fmt,omitempty"`
	YAxis  *AxisSpec `json:"y_axis,omitempty"`
}

type Series struct {
	Name   string    `json:"name,omitempty"`
	Values []float64 `json:"values,omitempty"`
}

type AxisSpec struct {
	Label string   `json:"label,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
}

// Column is a table column header plus the formatting every cell beneath it
// inherits.
type Column struct {
	Label string `json:"label"`

	// Fmt is text|number|currency|percent|date. Empty means the renderer
	// infers it from the column's own values.
	Fmt string `json:"fmt,omitempty"`

	// Align is left|center|right. Empty means the kind decides — numerics
	// right, everything else left.
	Align string `json:"align,omitempty"`

	// WidthWeight overrides the measured width for this column, relative to
	// the other columns' weights. Rarely needed: the renderer measures the
	// header and a sample of the cells.
	WidthWeight float64 `json:"width_weight,omitempty"`
}

// UnmarshalJSON accepts either a v2 object or a v1 bare string. This is the
// whole of the v1 table shim: `"columns": ["Item", "Qty"]` and
// `"columns": [{"label": "Item"}, {"label": "Qty", "fmt": "number"}]` both
// land in the same Go value.
func (c *Column) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		c.Label = s
		return nil
	}
	type raw Column // avoid recursing into this method
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*c = Column(r)
	return nil
}

// Cell is one value plus an optional per-cell format override.
type Cell struct {
	V   any    `json:"v"`
	Fmt string `json:"fmt,omitempty"`
}

// UnmarshalJSON accepts a scalar (v1: every cell was a string) or a {v, fmt}
// object (v2). Numbers decode as json.Number rather than float64 so a 19-digit
// invoice number survives the trip — float64 loses the last four digits of one
// and would print a subtly wrong id in a document someone pays against.
func (c *Cell) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	if b[0] == '{' {
		var r struct {
			V   json.RawMessage `json:"v"`
			Fmt string          `json:"fmt"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		c.Fmt = r.Fmt
		if len(r.V) == 0 {
			return nil
		}
		v, err := decodeScalar(r.V)
		if err != nil {
			return err
		}
		c.V = v
		return nil
	}
	v, err := decodeScalar(b)
	if err != nil {
		return err
	}
	c.V = v
	return nil
}

func decodeScalar(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// TextCell builds a cell for callers assembling a spec in Go.
func TextCell(s string) Cell { return Cell{V: s} }

// Table returns the section's inline table. Section-level columns/rows are the
// v1 shape and stay the v2 shape; Table exists so the renderer has one type to
// work with whether the table came from a section or from content.table.
func (s Section) Table() *Table {
	return &Table{
		Columns:  s.Columns,
		Rows:     s.Rows,
		TotalRow: s.TotalRow,
		Caption:  s.Caption,
	}
}

// V2 reports whether the document opted into the enterprise layout.
func (d *Document) V2() bool { return d.SpecVersion >= 2 }

// Cover returns the cover section, or nil. A v2 document without one renders
// without a cover page — an invoice does not want one, and the model should
// not be forced to ask for a page it does not need.
func (d *Document) Cover() *Section {
	for i := range d.Content.Sections {
		if d.Content.Sections[i].Type == SectionCover {
			return &d.Content.Sections[i]
		}
	}
	return nil
}

// Generated resolves GeneratedAt, falling back to now. The bool says which
// happened, because a caller writing a golden test needs to know that its
// bytes are not reproducible.
func (d *Document) Generated() (time.Time, bool) {
	if s := strings.TrimSpace(d.GeneratedAt); s != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
	}
	return time.Now(), false
}
