package canvas

import (
	"math"
	"sort"
	"strings"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/layout"
	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// A table on this surface is the same table that is in the report.
//
// It is typed the same way, formatted through the same package, and — this is
// the part that matters — proportioned by the same solver
// (internal/report/layout). A column that is 38% of the measure in the PDF is
// 38% of the measure here. The alternative is a reader with the deck open
// beside the report noticing that the two disagree about which column is the
// important one, which is a small thing that costs a lot of trust.
//
// What differs is the count, not the proportion: a slide holds about a dozen
// rows, so a table that pages across seventeen sheets of A4 becomes a run of
// continuation slides instead — and, in the video, a run of continuation
// scenes. Both get the run from RowsPerSurface, so the two cannot break a table
// in different places.
//
// This was the deck renderer's table.go until T-V1. Only the paging into slides
// stayed there; everything that decides a width, a type size or a string is
// here, where both renderers read it.

const (
	// MaxRowsPerSurface is the ceiling regardless of what fits. Twelve rows at
	// 18pt is already a slide people photograph rather than read; the height
	// check usually binds first anyway.
	MaxRowsPerSurface = 12

	// CellPadX and CellPadY are the breathing room inside a cell, per side.
	CellPadX = 3.0
	CellPadY = 1.8

	// MinColWidth keeps a narrow column readable at surface scale.
	MinColWidth = 18.0

	// MaxColShare caps one column at 45% of the measure, so a long free-text
	// column cannot squeeze the numbers it is describing into nothing.
	MaxColShare = 0.45

	// WidthQuantile is the point in a column's sorted cell widths taken as its
	// natural width. The maximum would let a single outlier own the table; the
	// mean would clip a fifth of the rows.
	WidthQuantile = 0.92

	// MaxCellLines bounds how far one cell may push a row before it is
	// truncated with a visible ellipsis.
	MaxCellLines = 2
)

// TableModel is a table resolved down to strings, widths and a type size —
// everything a renderer needs and nothing it has to decide.
//
// Widths and heights are millimetres on this surface; Px converts them for the
// video renderer. Fields are exported because two packages read them, not
// because anything outside this file should write one.
type TableModel struct {
	Caption string
	Header  []string
	Aligns  []string
	Widths  []float64 // millimetres, summing to the content width
	Rows    [][]string
	Total   []string
	Size    float64 // points
	RowH    float64 // millimetres
	HeaderH float64
}

// Alignment values, as the renderers write them.
const (
	AlignLeft   = "l"
	AlignCenter = "ctr"
	AlignRight  = "r"
)

// RowsPerSurface is how many body rows fit under the header in the body area.
func (m *TableModel) RowsPerSurface() int {
	avail := BodyHeight() - m.HeaderH
	if m.Caption != "" {
		avail -= TextHeight(m.Caption, theme.FontBody, measure.Regular, Type.Caption, ContentWidth()) + theme.Spacing.SM
	}
	// The total row has to fit beside the rows it totals.
	if len(m.Total) > 0 {
		avail -= m.RowH
	}
	n := int(avail / m.RowH)
	return max(1, min(n, MaxRowsPerSurface))
}

// BuildTable resolves columns, formats every cell and measures the result.
//
// It is the surface's counterpart to the PDF's assignWidths, and the two agree
// by sharing internal/report/layout rather than by both being careful.
func BuildTable(t *spec.Table, base format.Options) *TableModel {
	size := TableTextSize(len(t.Columns))

	kinds := make([]format.Kind, len(t.Columns))
	aligns := make([]string, len(t.Columns))
	opts := make([]format.Options, len(t.Columns))
	header := make([]string, len(t.Columns))

	for i, c := range t.Columns {
		values := columnValues(t, i)

		kind := format.KindText
		if c.Fmt != "" {
			kind = format.ParseKind(c.Fmt)
		} else {
			kind = format.InferKind(values)
		}
		kinds[i] = kind

		o := base
		if c.Fmt != "" || kind != format.KindText {
			// One decimal count for the whole column: 1,5 stacked above 1,50
			// above 2 is what a dumped table looks like.
			o.Decimals = format.ColumnDecimals(values, kind, o.Currency)
		}
		o.ShortDate = kind == format.KindDate
		opts[i] = o

		alignment := AlignLeft
		if kind.Numeric() {
			alignment = AlignRight
		}
		switch strings.ToLower(strings.TrimSpace(c.Align)) {
		case "right":
			alignment = AlignRight
		case "center", "centre":
			alignment = AlignCenter
		case "left":
			alignment = AlignLeft
		}
		aligns[i] = alignment
		header[i] = strings.TrimSpace(c.Label)
	}

	body := make([][]string, len(t.Rows))
	for i, row := range t.Rows {
		body[i] = formatRow(row, kinds, opts)
	}
	var total []string
	if len(t.TotalRow) > 0 {
		total = formatRow(t.TotalRow, kinds, opts)
	}

	widths := columnWidths(header, body, total, kinds, t.Columns, size)

	// Truncation happens after the widths are known: a cell is only too long
	// once there is a column width to be too long for.
	for i := range header {
		header[i] = FitLines(header[i], theme.FontBody, measure.Bold, size, widths[i]-2*CellPadX, MaxCellLines)
	}
	for i := range body {
		body[i] = fitRow(body[i], widths, size)
	}
	if total != nil {
		total = fitRow(total, widths, size)
	}

	m := &TableModel{
		Caption: strings.TrimSpace(t.Caption),
		Header:  header,
		Aligns:  aligns,
		Widths:  widths,
		Rows:    body,
		Total:   total,
		Size:    size,
	}
	m.HeaderH = RowHeight(size, maxLinesIn([][]string{header}, widths, size, measure.Bold))
	// A body row is as tall as the tallest cell anywhere in the table rather
	// than as tall as its own content. Rows of two different heights in one
	// table read as a formatting error on a slide, where there is no page break
	// to explain them.
	m.RowH = RowHeight(size, maxLinesIn(flatten(body, total), widths, size, measure.Regular))
	return m
}

// TableTextSize steps the type down as a table gets wider. Eight columns of
// 18pt across 291mm is 36mm a column, which is not enough for a rupiah figure,
// so the alternative to stepping down is truncating every number in the table.
func TableTextSize(cols int) float64 {
	switch {
	case cols <= 4:
		return Type.Body
	case cols <= 6:
		return math.Round((Type.Body+Type.Caption)/2*2) / 2
	default:
		return Type.Caption
	}
}

// RowHeight is the height of a row of the given line count at the given size.
func RowHeight(size float64, lines int) float64 {
	return float64(max(lines, 1))*measure.LineHeightMM(size)*BodyLeading + 2*CellPadY
}

// CellText formats one spec cell. The cell's own fmt wins over the column's,
// which is how a total row states its currency in a column of plain numbers.
func CellText(c spec.Cell, fallback format.Kind, opts format.Options) string {
	kind := fallback
	if c.Fmt != "" {
		kind = format.ParseKind(c.Fmt)
	}
	return format.Value(c.V, kind, opts)
}

func formatRow(row []spec.Cell, kinds []format.Kind, opts []format.Options) []string {
	out := make([]string, len(kinds))
	for i := range kinds {
		if i >= len(row) {
			continue
		}
		out[i] = CellText(row[i], kinds[i], opts[i])
	}
	return out
}

func maxLinesIn(rows [][]string, widths []float64, size float64, style measure.Style) int {
	lines := 1
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			lines = max(lines, min(LinesIn(cell, theme.FontBody, style, size, widths[i]-2*CellPadX), MaxCellLines))
		}
	}
	return lines
}

func flatten(rows [][]string, extra []string) [][]string {
	if extra == nil {
		return rows
	}
	return append(append([][]string{}, rows...), extra)
}

// columnWidths measures each column and hands the result to the shared solver.
// The rigid/flexible distinction is the PDF's, for the PDF's reasons: numbers
// and dates either fit on one line or wrap into nonsense, and a token with no
// space in it does not narrow, it truncates.
func columnWidths(header []string, body [][]string, total []string, kinds []format.Kind, cols []spec.Column, size float64) []float64 {
	n := len(header)
	natural := make([]float64, n)
	rigid := make([]bool, n)
	maxWidth := ContentWidth() * MaxColShare

	for i := range n {
		rigid[i] = kinds[i].Numeric() || kinds[i] == format.KindDate || cols[i].WidthWeight > 0

		if cols[i].WidthWeight > 0 {
			natural[i] = math.Min(cols[i].WidthWeight*ContentWidth()/float64(n), maxWidth)
			continue
		}

		w := measure.Width(header[i], theme.FontBody, measure.Bold, size)
		samples := make([]float64, 0, len(body))
		unbreakable, seen := 0, 0
		for j := range body {
			if i >= len(body[j]) {
				continue
			}
			value := body[j][i]
			samples = append(samples, measure.Width(value, theme.FontBody, measure.Regular, size))
			if strings.TrimSpace(value) == "" {
				continue
			}
			seen++
			if !strings.Contains(strings.TrimSpace(value), " ") {
				unbreakable++
			}
		}
		if seen > 0 && unbreakable == seen {
			rigid[i] = true
		}
		if len(samples) > 0 {
			sort.Float64s(samples)
			idx := int(math.Round(WidthQuantile * float64(len(samples)-1)))
			w = max(w, samples[idx])
		}
		if i < len(total) {
			w = max(w, measure.Width(total[i], theme.FontBody, measure.Bold, size))
		}
		// The padding, plus the slack a substituted face may need. A rigid
		// column has nowhere to reflow to, so paying for the substitution up
		// front is the difference between a number that fits and a number with
		// an ellipsis in the middle of it.
		w = w/SubstitutionMargin + 2*CellPadX
		natural[i] = math.Min(w, maxWidth)
	}

	weights := layout.Allocate(natural, rigid, ContentWidth(), MinColWidth)
	return layout.Scale(weights, ContentWidth(), MinColWidth)
}

func fitRow(cells []string, widths []float64, size float64) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if i >= len(widths) {
			out[i] = c
			continue
		}
		out[i] = FitLines(c, theme.FontBody, measure.Regular, size, widths[i]-2*CellPadX, MaxCellLines)
	}
	return out
}

// columnValues collects a column's raw values for type inference, including the
// total row: a total is the value most likely to be the widest, and a column
// typed without it can be typed wrong.
func columnValues(t *spec.Table, i int) []any {
	values := make([]any, 0, len(t.Rows)+1)
	for _, row := range t.Rows {
		if i < len(row) {
			values = append(values, row[i].V)
		}
	}
	if i < len(t.TotalRow) {
		values = append(values, t.TotalRow[i].V)
	}
	return values
}
