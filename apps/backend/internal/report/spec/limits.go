package spec

import (
	"fmt"
	"strings"
)

// Limits bounds a spec that arrived over HTTP (T-A2).
//
// Validate answers "is this spec coherent?"; this answers "is this spec
// something we are willing to spend a worker on?" They are separate because
// the agent's own spec has never needed the second question: it comes from a
// model with a context window, on the other side of a tool description that
// tells it to keep tables under eight columns. A spec posted to `/v1` comes
// from a `for` loop, and maroto will cheerfully attempt to lay out 500 000
// rows and take the process with it.
//
// Everything here is checked **before** a renderer is reached. A limit
// enforced during rendering is a limit that has already spent the memory.
type Limits struct {
	// MaxRows is the total across every table, sheet and table section in the
	// document — not per table. A caller splitting 500 000 rows into fifty
	// tables of ten thousand has not sent a smaller document.
	MaxRows int
	// MaxCols is per table. Wide is a layout problem rather than a memory one,
	// and it does not accumulate down the document.
	MaxCols int
	// MaxStringLen bounds one string. A single cell holding ten megabytes of
	// text is under any row cap ever written.
	MaxStringLen int
	// MaxSections bounds the content tree. Each section is a layout pass, so a
	// document of a million empty headings is expensive without a single row.
	MaxSections int
	// MaxChartPoints is the total across every chart. The chart renderer draws
	// each point, and a line chart with a million of them is a black rectangle
	// that took a minute to produce.
	MaxChartPoints int
}

// DefaultLimits is what the config's defaults resolve to. Exported so a test
// and the API agree on the numbers without one of them hard-coding them.
var DefaultLimits = Limits{
	MaxRows:        50_000,
	MaxCols:        40,
	MaxStringLen:   32_768,
	MaxSections:    2_000,
	MaxChartPoints: 20_000,
}

// Normalize replaces non-positive fields with the defaults, matching how every
// other constructor in this codebase treats bad input. A zero limit meaning
// "unlimited" would turn a forgotten config value into the exact failure these
// exist to prevent.
func (l Limits) Normalize() Limits {
	if l.MaxRows <= 0 {
		l.MaxRows = DefaultLimits.MaxRows
	}
	if l.MaxCols <= 0 {
		l.MaxCols = DefaultLimits.MaxCols
	}
	if l.MaxStringLen <= 0 {
		l.MaxStringLen = DefaultLimits.MaxStringLen
	}
	if l.MaxSections <= 0 {
		l.MaxSections = DefaultLimits.MaxSections
	}
	if l.MaxChartPoints <= 0 {
		l.MaxChartPoints = DefaultLimits.MaxChartPoints
	}
	return l
}

// LimitError names the limit that was hit and the field that hit it, so a
// handler can put a `param` in the error envelope and an integrator can fix
// the request without reading prose.
type LimitError struct {
	// Param is the JSON path of the offending field, e.g. `content.rows`.
	Param string
	// Limit and Got are the numbers, so the message does not have to be parsed
	// to learn how far over the caller was.
	Limit int
	Got   int
	msg   string
}

func (e *LimitError) Error() string { return e.msg }

// CheckLimits rejects an oversized spec.
//
// It walks the whole document rather than stopping at the first table: the
// count that matters is the total, and a caller who split their rows across
// sections is exactly the caller a per-table check would let through.
func CheckLimits(d *Document, l Limits) error {
	l = l.Normalize()

	if n := len(d.Content.Sections); n > l.MaxSections {
		return &LimitError{
			Param: "content.sections", Limit: l.MaxSections, Got: n,
			msg: fmt.Sprintf("content.sections has %d sections; the limit is %d", n, l.MaxSections),
		}
	}

	rows := 0
	points := 0

	check := func(param string, cols []Column, tableRows [][]Cell) error {
		if n := len(cols); n > l.MaxCols {
			return &LimitError{
				Param: param + ".columns", Limit: l.MaxCols, Got: n,
				msg: fmt.Sprintf("%s.columns has %d columns; the limit is %d", param, n, l.MaxCols),
			}
		}
		rows += len(tableRows)
		if rows > l.MaxRows {
			return &LimitError{
				Param: param + ".rows", Limit: l.MaxRows, Got: rows,
				msg: fmt.Sprintf("the document has at least %d rows; the limit is %d across the whole document", rows, l.MaxRows),
			}
		}
		for _, r := range tableRows {
			for _, cell := range r {
				if err := checkString(param+".rows", cell.V, l.MaxStringLen); err != nil {
					return err
				}
			}
		}
		for _, c := range cols {
			if err := checkString(param+".columns", c.Label, l.MaxStringLen); err != nil {
				return err
			}
		}
		return nil
	}

	if t := d.Content.Table; t != nil {
		if err := check("content.table", t.Columns, t.Rows); err != nil {
			return err
		}
	}
	for i := range d.Content.Sheets {
		sh := &d.Content.Sheets[i]
		if err := check(fmt.Sprintf("content.sheets[%d]", i), sh.Columns, sh.Rows); err != nil {
			return err
		}
	}
	for i := range d.Content.Sections {
		s := &d.Content.Sections[i]
		param := fmt.Sprintf("content.sections[%d]", i)
		if len(s.Columns) > 0 || len(s.Rows) > 0 {
			if err := check(param, s.Columns, s.Rows); err != nil {
				return err
			}
		}
		// Every free-text field on a section, not only the ones a given type
		// reads: an unused field still arrived in the body and still has to be
		// held in memory, and a caller sending ten megabytes of `subtitle` on a
		// page_break is not doing so by accident.
		for _, str := range []string{s.Text, s.Title, s.Subtitle, s.Period, s.PreparedFor, s.PreparedBy, s.Confidentiality, s.Caption} {
			if err := checkString(param, str, l.MaxStringLen); err != nil {
				return err
			}
		}
		for _, it := range s.Items {
			if err := checkString(param+".items", it.K, l.MaxStringLen); err != nil {
				return err
			}
			if err := checkString(param+".items", it.V, l.MaxStringLen); err != nil {
				return err
			}
			if err := checkString(param+".items", it.Label, l.MaxStringLen); err != nil {
				return err
			}
		}
		if s.Chart != nil {
			for _, ser := range s.Chart.Series {
				points += len(ser.Values)
			}
			if points > l.MaxChartPoints {
				return &LimitError{
					Param: param + ".chart.series", Limit: l.MaxChartPoints, Got: points,
					msg: fmt.Sprintf("the document plots at least %d chart points; the limit is %d", points, l.MaxChartPoints),
				}
			}
		}
	}

	if err := checkString("title", d.Title, l.MaxStringLen); err != nil {
		return err
	}
	return checkString("filename", d.Filename, l.MaxStringLen)
}

// checkString measures anything that decoded as a string. A cell holding a
// number or a bool has a bounded rendering and needs no check; only a string
// can be arbitrarily long.
func checkString(param string, v any, max int) error {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	if len(s) <= max {
		return nil
	}
	return &LimitError{
		Param: param, Limit: max, Got: len(s),
		msg: fmt.Sprintf("%s holds a %d-byte string; the limit is %d per value", param, len(s), max),
	}
}

// TotalRows counts the rows a document carries, for a log line that says how
// big the thing we just rendered was. Kept beside the limits so the two agree
// on what "a row" means.
func TotalRows(d *Document) int {
	n := 0
	if d.Content.Table != nil {
		n += len(d.Content.Table.Rows)
	}
	for i := range d.Content.Sheets {
		n += len(d.Content.Sheets[i].Rows)
	}
	for i := range d.Content.Sections {
		n += len(d.Content.Sections[i].Rows)
	}
	return n
}

// FormatOf is the document's format as a trimmed lower-case string, for
// callers that have not called Normalize yet.
func FormatOf(d *Document) string { return strings.ToLower(strings.TrimSpace(d.Format)) }
