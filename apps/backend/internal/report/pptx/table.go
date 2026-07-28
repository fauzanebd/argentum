package pptx

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

// A table on a slide is the same table that is in the report.
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
// continuation slides instead.

const (
	// maxRowsPerSlide is the ceiling regardless of what fits. Twelve rows at
	// 18pt is already a slide people photograph rather than read; the height
	// check below usually binds first anyway.
	maxRowsPerSlide = 12

	// cellPadX and cellPadY are the breathing room inside a cell, per side.
	cellPadX = 3.0
	cellPadY = 1.8

	// minColWidth keeps a narrow column readable at slide scale.
	minColWidth = 18.0

	// maxColShare caps one column at 45% of the measure, so a long free-text
	// column cannot squeeze the numbers it is describing into nothing.
	maxColShare = 0.45

	// widthQuantile is the point in a column's sorted cell widths taken as its
	// natural width. The maximum would let a single outlier own the table; the
	// mean would clip a fifth of the rows.
	widthQuantile = 0.92

	// maxCellLines bounds how far one cell may push a row before it is
	// truncated with a visible ellipsis.
	maxCellLines = 2
)

// tableModel is a table resolved down to strings, widths and a type size —
// everything the slide writer needs and nothing it has to decide.
type tableModel struct {
	caption string
	header  []string
	aligns  []string
	widths  []float64 // millimetres, summing to the content width
	rows    [][]string
	total   []string
	size    float64 // points
	rowH    float64 // millimetres
	headerH float64
}

func (b *builder) table(t *spec.Table) error {
	if t == nil || len(t.Columns) == 0 {
		return nil
	}
	title := b.slideTitle()
	m := b.r.buildTable(t)

	rows := m.rowsPerSlide()
	pages := chunk(m.rows, rows)
	if len(pages) == 0 {
		pages = [][][]string{nil}
	}

	for i, page := range pages {
		part := *m
		part.rows = page
		// The total row belongs on the last slide of the run, under the last
		// rows it totals. Repeating it on every continuation would state the
		// same sum three times against three different subsets of the data.
		if i < len(pages)-1 {
			part.total = nil
		}
		b.slides = append(b.slides, slide{
			kind:      kindTable,
			title:     title,
			subtitle:  m.caption,
			table:     &part,
			continued: i > 0,
		})
	}
	return nil
}

// rowsPerSlide is how many body rows fit under the header in the body area.
func (m *tableModel) rowsPerSlide() int {
	avail := bodyHeight() - m.headerH
	if m.caption != "" {
		avail -= textHeight(m.caption, theme.FontBody, measure.Regular, deckType.Caption, contentWidth()) + theme.Spacing.SM
	}
	// The total row has to fit beside the rows it totals.
	if len(m.total) > 0 {
		avail -= m.rowH
	}
	n := int(avail / m.rowH)
	return max(1, min(n, maxRowsPerSlide))
}

// buildTable resolves columns, formats every cell and measures the result.
//
// It is the deck's counterpart to the PDF's assignWidths, and the two agree by
// sharing internal/report/layout rather than by both being careful.
func (r *renderer) buildTable(t *spec.Table) *tableModel {
	size := tableTextSize(len(t.Columns))

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

		o := r.fmt
		if c.Fmt != "" || kind != format.KindText {
			// One decimal count for the whole column: 1,5 stacked above 1,50
			// above 2 is what a dumped table looks like.
			o.Decimals = format.InferDecimals(values)
			if kind == format.KindCurrency || kind == format.KindPercent {
				o.Decimals = format.AutoDecimals
			}
		}
		o.ShortDate = kind == format.KindDate
		opts[i] = o

		alignment := alignLeft
		if kind.Numeric() {
			alignment = alignRight
		}
		switch strings.ToLower(strings.TrimSpace(c.Align)) {
		case "right":
			alignment = alignRight
		case "center", "centre":
			alignment = alignCenter
		case "left":
			alignment = alignLeft
		}
		aligns[i] = alignment
		header[i] = strings.TrimSpace(c.Label)
	}

	body := make([][]string, len(t.Rows))
	for i, row := range t.Rows {
		body[i] = r.formatRow(row, kinds, opts)
	}
	var total []string
	if len(t.TotalRow) > 0 {
		total = r.formatRow(t.TotalRow, kinds, opts)
	}

	widths := columnWidths(header, body, total, kinds, t.Columns, size)

	// Truncation happens after the widths are known: a cell is only too long
	// once there is a column width to be too long for.
	for i := range header {
		header[i] = fitLines(header[i], theme.FontBody, measure.Bold, size, widths[i]-2*cellPadX, maxCellLines)
	}
	for i := range body {
		body[i] = fitRow(body[i], widths, size)
	}
	if total != nil {
		total = fitRow(total, widths, size)
	}

	m := &tableModel{
		caption: strings.TrimSpace(t.Caption),
		header:  header,
		aligns:  aligns,
		widths:  widths,
		rows:    body,
		total:   total,
		size:    size,
	}
	m.headerH = rowHeight(size, maxLinesIn([][]string{header}, widths, size, measure.Bold))
	// A body row is as tall as the tallest cell anywhere in the table rather
	// than as tall as its own content. Rows of two different heights in one
	// table read as a formatting error on a slide, where there is no page break
	// to explain them.
	m.rowH = rowHeight(size, maxLinesIn(flatten(body, total), widths, size, measure.Regular))
	return m
}

// tableTextSize steps the type down as a table gets wider. Eight columns of
// 18pt across 291mm is 36mm a column, which is not enough for a rupiah figure,
// so the alternative to stepping down is truncating every number in the table.
func tableTextSize(cols int) float64 {
	switch {
	case cols <= 4:
		return deckType.Body
	case cols <= 6:
		return math.Round((deckType.Body+deckType.Caption)/2*2) / 2
	default:
		return deckType.Caption
	}
}

func rowHeight(size float64, lines int) float64 {
	return float64(max(lines, 1))*measure.LineHeightMM(size)*bodyLeading + 2*cellPadY
}

func maxLinesIn(rows [][]string, widths []float64, size float64, style measure.Style) int {
	lines := 1
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			lines = max(lines, min(linesIn(cell, theme.FontBody, style, size, widths[i]-2*cellPadX), maxCellLines))
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
	maxWidth := contentWidth() * maxColShare

	for i := range n {
		rigid[i] = kinds[i].Numeric() || kinds[i] == format.KindDate || cols[i].WidthWeight > 0

		if cols[i].WidthWeight > 0 {
			natural[i] = math.Min(cols[i].WidthWeight*contentWidth()/float64(n), maxWidth)
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
			idx := int(math.Round(widthQuantile * float64(len(samples)-1)))
			w = max(w, samples[idx])
		}
		if i < len(total) {
			w = max(w, measure.Width(total[i], theme.FontBody, measure.Bold, size))
		}
		// The padding, plus the slack a substituted face may need. A rigid
		// column has nowhere to reflow to, so paying for the substitution up
		// front is the difference between a number that fits and a number with
		// an ellipsis in the middle of it.
		w = w/substitutionMargin + 2*cellPadX
		natural[i] = math.Min(w, maxWidth)
	}

	weights := layout.Allocate(natural, rigid, contentWidth(), minColWidth)
	return layout.Scale(weights, contentWidth(), minColWidth)
}

func fitRow(cells []string, widths []float64, size float64) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if i >= len(widths) {
			out[i] = c
			continue
		}
		out[i] = fitLines(c, theme.FontBody, measure.Regular, size, widths[i]-2*cellPadX, maxCellLines)
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

func (r *renderer) formatRow(row []spec.Cell, kinds []format.Kind, opts []format.Options) []string {
	out := make([]string, len(kinds))
	for i := range kinds {
		if i >= len(row) {
			continue
		}
		out[i] = r.cellText(row[i], kinds[i], opts[i])
	}
	return out
}

// cellText formats one spec cell. The cell's own fmt wins over the column's,
// which is how a total row states its currency in a column of plain numbers.
func (r *renderer) cellText(c spec.Cell, fallback format.Kind, opts format.Options) string {
	kind := fallback
	if c.Fmt != "" {
		kind = format.ParseKind(c.Fmt)
	}
	return format.Value(c.V, kind, opts)
}
