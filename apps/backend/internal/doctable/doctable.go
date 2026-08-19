// Package doctable turns a parser's grid of strings into typed columns (T-P4).
//
// **This is where the accuracy of the whole PDF feature is won or lost, and no
// public benchmark scores it.** The parsers in the roadmap's §2 are measured on
// how well they render a page as markdown; none of them is measured on whether
// the figure that ends up in a database is the figure that was printed. Between
// a table candidate and a source sit seven failure families, and six of them
// produce a number that is wrong and looks right:
//
//   - locale numerals — "1.234" is one number in Jakarta and another in London;
//   - header scale words — "dalam jutaan" makes every cell below it a million;
//   - accounting negatives — "(1.234)" is minus one thousand two hundred;
//   - footnote markers — "1.234²" is not 1,234 raised to anything;
//   - merged headers — two header rows are one column name;
//   - continuation — a table split over three pages is one table;
//   - total rows — the seventh, and the only one that fails loudly: a TOTAL
//     row loaded as data double-counts every aggregate built on it.
//
// Everything here is a pure function over the parse artifact. That is
// deliberate and it is the ticket's own instruction: this is the most testable
// code in the roadmap and the code where a mistake is most expensive, so the
// table tests are the deliverable rather than the decoration.
//
// **No model is called from this package, ever.** A model asked to type a
// column is a model that will confidently type the one column it cannot read,
// and the failure would be invisible: a plausible number in a plausible column.
// Deterministic code that refuses — a column with one unreadable cell becomes
// text rather than dropping the cell — is worth more than a clever one that
// guesses.
package doctable

import (
	"strconv"
	"strings"

	"github.com/fauzanebd/argentum/internal/docparse"
)

// ColumnType is what a column holds, decided from every cell in it.
//
// There is no "mixed". A column is numeric only when *every* non-empty cell
// parses; one cell that does not makes the whole column text. The alternative —
// typing a 95%-numeric column as numeric and nulling the rest — is how a
// figure with a footnote marker on it disappears from a total, and a figure
// that disappears is worse than a column that has to be read as strings.
type ColumnType string

const (
	ColumnText       ColumnType = "text"
	ColumnInteger    ColumnType = "integer"
	ColumnDecimal    ColumnType = "decimal"
	ColumnCurrency   ColumnType = "currency"
	ColumnPercentage ColumnType = "percentage"
	ColumnDate       ColumnType = "date"
)

// Numeric reports whether this type is one whose values are compared, summed
// and quarantined by the arithmetic check (T-P5).
func (t ColumnType) Numeric() bool {
	switch t {
	case ColumnInteger, ColumnDecimal, ColumnCurrency, ColumnPercentage:
		return true
	default:
		return false
	}
}

// Column is one typed column of an extracted table.
type Column struct {
	// Header is what the document called it, with a multi-row header resolved
	// into one string: "Q4 2024 › Actual". The separator is a character no
	// header uses, so a reviewer can see the join and a later reader can undo it.
	Header string `json:"header"`
	// Name is the identifier the warehouse table gets. Slugified from Header,
	// because a column name reaches a model in `get_schema` and has to be
	// legible as well as safe.
	Name string     `json:"name"`
	Type ColumnType `json:"type"`
	// Multiplier is the header-level scale word, 1 when there is none. Recorded
	// rather than silently folded into the values, because an unrecorded
	// multiplier is unauditable: a reviewer looking at 3,863 in the review grid
	// and 3,863,000,000 in the warehouse has no way to tell whether that was
	// intended.
	Multiplier float64 `json:"multiplier"`
	// MultiplierSource is the phrase that produced it — "dalam jutaan Rupiah" —
	// so the review surface can show what it read rather than only what it
	// concluded.
	MultiplierSource string `json:"multiplier_source,omitempty"`
	// Decimal is the separator this column voted for, '.' or ',', 0 for a text
	// column. Carried because it is the single decision most likely to be wrong
	// by a factor of a thousand, and a decision nobody can see is a decision
	// nobody can overrule.
	Decimal byte `json:"-"`
	// Currency is the symbol or code seen on the cells, when there was one.
	Currency string `json:"currency,omitempty"`
	// Precision is how many decimal places the document *printed*. It is the
	// tolerance the arithmetic check compares at — not a display hint.
	Precision int `json:"precision"`
	// PII is the class T-P12 assigned at publish: "", "identity" or "contact".
	PII string `json:"pii,omitempty"`
}

// Cell is one cell, kept with its raw text whatever its type.
//
// The raw string survives typing on purpose. It is what the review surface
// shows beside the page, it is what a mismatch message quotes, and it is the
// only way to answer "what did the document actually print here?" after a
// multiplier has been applied.
type Cell struct {
	Raw string `json:"raw"`
	// Num is the parsed value, multiplier applied, when the column is numeric
	// and this cell is not empty.
	Num *float64 `json:"num,omitempty"`
	// Date is the ISO-8601 rendering when the column typed as a date.
	Date string `json:"date,omitempty"`
}

// Row is one row of data, and where in the document it came from.
//
// The provenance is not bookkeeping: T-P6 materializes `source_page` and
// `source_row` as real columns, so a figure that looks wrong is one query away
// from the page that produced it. A row that cannot say where it came from is
// a row nobody can check.
type Row struct {
	Cells []Cell `json:"cells"`
	Page  int    `json:"page"`
	// Total is empty for a data row, and says how this row was recognised as a
	// total when it is one: "label" for a row whose first cell says TOTAL,
	// "arithmetic" for one whose numbers turned out to be the sum of the rows
	// above it. The distinction is not cosmetic — T-P5 declines to count the
	// second as evidence, because a row identified *by* adding up cannot then
	// prove that the table adds up.
	Total string `json:"total,omitempty"`
	// Index is the row's position in the candidate grid on that page, header
	// rows included, so it addresses the page rather than the extraction.
	Index int `json:"index"`
}

// Table is one extracted table: typed columns, data rows, and the rows this
// package refused to treat as data.
type Table struct {
	// Title is the caption found above the grid, when there was one. It is also
	// where a scale word most often hides.
	Title     string   `json:"title"`
	FirstPage int      `json:"first_page"`
	LastPage  int      `json:"last_page"`
	Columns   []Column `json:"columns"`
	Rows      []Row    `json:"rows"`
	// Totals are the rows that state the document's own answer. Flagged and
	// held aside — never silently dropped, because a total row is the evidence
	// T-P5 checks the parse against, and never in Rows, because a TOTAL loaded
	// as data double-counts every aggregate built on it.
	Totals []Row `json:"totals"`
	// Boxes is where the table sat, one rectangle per page it spans.
	Boxes []PageBox `json:"boxes,omitempty"`
	// Candidate is the parser's index for this grid on its first page. With
	// FirstPage it forms [Table.Key], which is how a re-parse finds the draft a
	// reviewer has been editing instead of creating a second one beside it.
	Candidate int `json:"candidate"`
	// Strategy is the parser's: `lines` when ruling lines defined the grid,
	// `text` when word alignment did. It reaches the reviewer because the
	// second is an inference.
	Strategy string `json:"strategy"`
	// Notes are what this package did that a reviewer would not otherwise see —
	// a caption dropped, three pages joined, a column forced to text by one
	// cell. Written for a person, in order.
	Notes []string `json:"notes,omitempty"`
	// Verify is T-P5's outcome. Zero value is an unverified table, which is a
	// legal state: most tables state no total to check.
	Verify Verification `json:"verify"`
}

// Options tunes the extraction. The zero value is the shipped behaviour.
type Options struct {
	// MaxHeaderRows bounds how many leading rows may be merged into one header.
	// Three is enough for "2024 / Q4 / Actual" and small enough that a table
	// whose first data rows are text cannot eat itself.
	MaxHeaderRows int
	// MinRows is how many data rows a candidate needs to be worth keeping. Two
	// consecutive prose lines look like a two-by-two grid to the text strategy,
	// and the roadmap's own gate produced exactly that.
	MinRows int
}

func (o Options) withDefaults() Options {
	if o.MaxHeaderRows <= 0 {
		o.MaxHeaderRows = 3
	}
	if o.MinRows <= 0 {
		o.MinRows = 1
	}
	return o
}

// Build turns every table candidate on every page into typed tables, joining
// the ones that continue across a page break.
//
// Pages are taken in the order the parser returned them. A page the parser
// could not read contributes nothing and does not interrupt a continuation: a
// scan in the middle of a born-digital report is a page nobody read, not a
// reason to split the table around it into two sources.
func Build(pages []docparse.Page, opts Options) []Table {
	opts = opts.withDefaults()

	var tables []Table
	for _, page := range pages {
		if page.Kind != docparse.KindText {
			continue
		}
		for _, candidate := range page.Tables {
			t, ok := build(page, candidate, opts)
			if !ok {
				continue
			}
			if prev := len(tables) - 1; prev >= 0 && continues(tables[prev], t) {
				tables[prev] = join(tables[prev], t)
				continue
			}
			tables = append(tables, t)
		}
	}

	for i := range tables {
		retype(&tables[i])
		// What kind of personal data this table holds (T-P12), decided after
		// typing because the cell patterns read the raw values and the header
		// hints read the resolved header — both of which the typing pass is
		// what settles.
		ClassifyPII(&tables[i])
		tables[i].Verify = Verify(tables[i])
	}
	return tables
}

// build turns one candidate grid into a table, or reports that it is not one.
func build(page docparse.Page, candidate docparse.Table, opts Options) (Table, bool) {
	grid := trimEmpty(candidate.Rows)
	if len(grid) == 0 {
		return Table{}, false
	}

	caption, grid, dropped := stripCaption(grid, candidate.ColCount)
	headerRows, body := splitHeader(grid, opts.MaxHeaderRows)
	if len(body) < opts.MinRows {
		return Table{}, false
	}

	t := Table{
		Title:     firstNonEmpty(caption, captionAbove(page, candidate)),
		FirstPage: page.Number,
		LastPage:  page.Number,
		Strategy:  candidate.Strategy,
		Columns:   resolveHeader(headerRows, width(grid)),
		Boxes:     []PageBox{{Page: page.Number, BBox: candidate.BBox}},
		Candidate: candidate.Index,
	}
	t.Notes = append(t.Notes, dropped...)

	for i, raw := range body {
		row := Row{Page: page.Number, Index: len(headerRows) + i + len(dropped)}
		for c := 0; c < len(t.Columns); c++ {
			cell := ""
			if c < len(raw) {
				cell = cleanText(raw[c])
			}
			row.Cells = append(row.Cells, Cell{Raw: cell})
		}
		if isTotalRow(row, t.Columns) {
			row.Total = "label"
			t.Totals = append(t.Totals, row)
			continue
		}
		t.Rows = append(t.Rows, row)
	}
	if len(t.Rows) == 0 {
		// Every row was a total. That is not a table; it is a summary line the
		// text strategy found a grid in, and publishing it would produce a
		// source whose only rows are the ones that must never be summed.
		return Table{}, false
	}

	// The scale word is looked for on the page rather than in the grid, because
	// it is usually not in the grid: a caption above the table, a note below it,
	// or the header cell of one column.
	applyScale(&t, page, candidate)
	return t, true
}

// Key identifies this table across re-parses of the same document: the page it
// starts on and the parser's index for it there.
//
// Stable enough to be useful and honest about what it is. A document re-parsed
// by a better parser can produce a different set of candidates, and when it
// does, a draft whose key no longer matches is a draft about a table that is no
// longer there — which the review surface shows as gone rather than silently
// re-pointing at whatever took its place.
func (t Table) Key() string {
	return "p" + strconv.Itoa(t.FirstPage) + "-c" + strconv.Itoa(t.Candidate)
}

// Revalue re-reads a table's cells under a reviewer's column decisions and
// re-runs the arithmetic check (T-P7 → T-P6).
//
// This is what makes a type override mean something. The columns a reviewer
// edited carry a different type, multiplier or decimal separator from the ones
// this package inferred, and every cell value below them has to be read again —
// including the total rows, because a multiplier that changes the parts changes
// what the stated total is being compared against.
//
// Columns are matched by position, which is the only thing that survives an
// edit: a reviewer may rename a column, and a rename that silently re-pointed
// the values would be the worst possible outcome of a text field.
func Revalue(t Table, cols []Column) Table {
	if len(cols) > 0 {
		out := make([]Column, len(t.Columns))
		copy(out, t.Columns)
		for i := range out {
			if i >= len(cols) {
				break
			}
			out[i] = cols[i]
			if out[i].Multiplier == 0 {
				out[i].Multiplier = 1
			}
			if out[i].Name == "" {
				out[i].Name = columnName(out[i].Header, i)
			}
		}
		t.Columns = out
	}
	for i := range t.Rows {
		valueRow(t.Columns, &t.Rows[i])
	}
	for i := range t.Totals {
		valueRow(t.Columns, &t.Totals[i])
	}
	// Re-classified rather than trusted from the stored columns: a reviewer can
	// edit a header, and a PII label that survived a rename would be describing
	// a column that no longer says what it said.
	ClassifyPII(&t)
	t.Verify = Verify(t)
	return t
}

// retype runs the typing pass over a table that is finished growing.
//
// It happens after the continuation join rather than during it, and that is the
// point: a column's type and its decimal separator are decided by majority over
// *every* cell in it, and a three-page table typed page by page can reach three
// different answers for one column.
func retype(t *Table) {
	all := make([]Row, 0, len(t.Rows)+len(t.Totals))
	all = append(all, t.Rows...)
	all = append(all, t.Totals...)

	for c := range t.Columns {
		col := &t.Columns[c]
		typeColumn(col, all, c)
		if col.Name == "" {
			col.Name = columnName(col.Header, c)
		}
	}
	dedupeNames(t.Columns)

	for i := range t.Rows {
		valueRow(t.Columns, &t.Rows[i])
	}
	for i := range t.Totals {
		valueRow(t.Columns, &t.Totals[i])
	}
	promoteArithmeticTotal(t)

	for c := range t.Columns {
		if t.Columns[c].Type != ColumnText {
			continue
		}
		// A multiplier on a text column is a statement about numbers that are
		// not there. Cleared rather than carried, so the review surface does not
		// offer a reviewer a scale factor for a column of product names.
		t.Columns[c].Multiplier = 1
		t.Columns[c].MultiplierSource = ""
	}
	for c := range t.Columns {
		if t.Columns[c].Type == ColumnText {
			continue
		}
		if note := typeNote(t.Columns[c]); note != "" {
			t.Notes = append(t.Notes, note)
		}
	}
}

func typeNote(col Column) string {
	if col.Multiplier == 1 {
		return ""
	}
	return "column " + col.Name + " was multiplied by " + trimFloat(col.Multiplier) +
		" because the document says " + strings.TrimSpace(col.MultiplierSource)
}

// trimEmpty drops rows that hold nothing at all. The lines strategy emits them
// for a ruled row with no content, and they are not data rows with missing
// values — they are the parser describing a horizontal rule.
func trimEmpty(rows [][]string) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func width(grid [][]string) int {
	w := 0
	for _, r := range grid {
		if len(r) > w {
			w = len(r)
		}
	}
	return w
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
