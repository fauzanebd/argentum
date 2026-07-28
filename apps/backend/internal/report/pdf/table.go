package pdf

import (
	"math"
	"sort"
	"strings"

	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/layout"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Table layout, in the order the decisions are made:
//
//  1. Each column is typed — from its declared fmt, or inferred from its own
//     values when the model did not declare one.
//  2. Every cell is formatted through internal/report/format, so a column of
//     rupiah has one separator convention and one decimal count regardless of
//     how the model wrote each figure.
//  3. Columns are measured, not divided. The old renderer gave every column
//     an equal share of a 12-unit grid, which is why an eight-column table
//     with one long product name was unreadable.
//  4. Rows are as tall as their tallest cell, and the table pages itself so
//     the header repeats and never stands alone at the foot of a page.

const (
	// cellPadX is the horizontal breathing room inside a cell, per side.
	cellPadX = 1.6

	// maxCellLines bounds how far one cell may push a row. Past three lines a
	// table has become a layout, and the cell is truncated with an ellipsis —
	// visibly, because silent clipping is an acceptance failure.
	maxCellLines = 3

	// minColUnits keeps a narrow column readable: below roughly 9mm even
	// "Qty" wraps.
	minColUnits = 6

	// maxColShare caps one column at 45% of the measure, so a long free-text
	// column cannot squeeze the numbers it is describing into nothing.
	maxColShare = 0.45

	// widthQuantile is the point in a column's sorted cell widths taken as its
	// natural width. The maximum would let a single outlier own the table;
	// the mean would clip a fifth of the rows.
	widthQuantile = 0.92
)

// tableColumn is a resolved column: what it holds, how it is written, how wide
// it is.
type tableColumn struct {
	label  string
	kind   format.Kind
	align  align.Type
	opts   format.Options
	weight float64
	units  int
}

func (r *renderer) renderTable(t *spec.Table) error {
	if t == nil || len(t.Columns) == 0 {
		return nil
	}

	cols := r.resolveColumns(t)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.label
	}
	body := r.formatRows(t.Rows, cols)
	var totals []string
	if len(t.TotalRow) > 0 {
		totals = r.formatRow(t.TotalRow, cols)
	}

	r.assignWidths(cols, header, body, totals)

	// Truncation happens after widths are known: a cell is only too long once
	// there is a column width to be too long for.
	for i := range header {
		header[i] = fitText(header[i], theme.FontMedium, fontstyle.Normal, theme.TypeScale.Body,
			colWidth(cols[i].units)-2*cellPadX, 2)
	}
	for i := range body {
		body[i] = truncateRow(body[i], cols)
	}
	if totals != nil {
		totals = truncateRow(totals, cols)
	}

	if caption := strings.TrimSpace(t.Caption); caption != "" {
		l := &rowList{}
		l.text(caption, props.Text{
			Family: theme.FontMedium,
			Size:   theme.TypeScale.Body,
			Color:  theme.ColorForeground.Props(),
			Align:  align.Left,
		}, contentWidth())
		l.space(theme.Spacing.XS)
		r.ensure(l.total + theme.Page.TableHeaderHeight + theme.Page.TableRowHeight)
		r.m.AddRows(l.rows...)
	}

	heights := make([]float64, len(body))
	for i, cells := range body {
		heights[i] = rowHeightFor(cells, cols, theme.FontBody)
	}

	headerHeight := max(theme.Page.TableHeaderHeight, rowHeightFor(header, cols, theme.FontMedium))

	// A header row with nothing under it is the one break this renderer never
	// makes: the first body row has to fit beside it or both move on.
	first := theme.Page.TableRowHeight
	if len(heights) > 0 {
		first = heights[0]
	}
	r.space(theme.Spacing.XS)
	r.ensure(headerHeight + first)
	r.emitTableHeader(header, cols, headerHeight)

	for i, cells := range body {
		if !r.fits(heights[i]) {
			r.breakPage()
			r.emitTableHeader(header, cols, headerHeight)
		}
		r.emitTableRow(cells, cols, heights[i], i%2 == 1, false)
	}

	if totals != nil {
		h := max(theme.Page.TableRowHeight, rowHeightFor(totals, cols, theme.FontMedium))
		if !r.fits(h) {
			r.breakPage()
			r.emitTableHeader(header, cols, headerHeight)
		}
		r.emitTableRow(totals, cols, h, false, true)
	}
	r.space(theme.Spacing.SM)
	return nil
}

// resolveColumns types every column, preferring what the spec declared and
// falling back to what the data looks like.
func (r *renderer) resolveColumns(t *spec.Table) []tableColumn {
	out := make([]tableColumn, len(t.Columns))
	for i, c := range t.Columns {
		values := columnValues(t, i)

		kind := format.KindText
		if c.Fmt != "" {
			kind = format.ParseKind(c.Fmt)
		} else {
			kind = format.InferKind(values)
		}

		opts := r.fmt
		if c.Fmt != "" || kind != format.KindText {
			// One decimal count for the whole column: 1,5 stacked above 1,50
			// above 2 is what a dumped table looks like.
			opts.Decimals = format.InferDecimals(values)
			if kind == format.KindCurrency || kind == format.KindPercent {
				opts.Decimals = format.AutoDecimals
			}
		}
		// Dates are abbreviated inside tables and nowhere else. See
		// format.Options.ShortDate.
		opts.ShortDate = kind == format.KindDate

		alignment := align.Left
		if kind.Numeric() {
			alignment = align.Right
		}
		switch strings.ToLower(strings.TrimSpace(c.Align)) {
		case "right":
			alignment = align.Right
		case "center", "centre":
			alignment = align.Center
		case "left":
			alignment = align.Left
		}

		out[i] = tableColumn{
			label:  strings.TrimSpace(c.Label),
			kind:   kind,
			align:  alignment,
			opts:   opts,
			weight: c.WidthWeight,
		}
	}
	return out
}

// columnValues collects a column's raw values for type inference, including
// the total row: a total is the value most likely to be the widest, and a
// column typed without it can be typed wrong.
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

func (r *renderer) formatRows(rows [][]spec.Cell, cols []tableColumn) [][]string {
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = r.formatRow(row, cols)
	}
	return out
}

func (r *renderer) formatRow(row []spec.Cell, cols []tableColumn) []string {
	out := make([]string, len(cols))
	for i := range cols {
		if i >= len(row) {
			continue
		}
		out[i] = r.cellText(row[i], cols[i].kind, cols[i].opts)
	}
	return out
}

// assignWidths measures the header and a sample of the body, then turns the
// measurements into integer grid units that sum to exactly the grid.
//
// The measurement is not divided evenly across columns, because columns are
// not equally compressible. "Rp 121.000" and "1 Januari 2026" either fit on
// one line or wrap into nonsense; a customer name gives up a word and reads
// fine. So numeric and date columns are served their natural width first —
// capped — and the prose columns share whatever is left. Proportional scaling
// of everything, which is what the first version of this did, wraps every
// currency cell in an eight-column table onto two lines while a text column
// keeps space it did not need.
func (r *renderer) assignWidths(cols []tableColumn, header []string, body [][]string, totals []string) {
	natural := make([]float64, len(cols))
	rigid := make([]bool, len(cols))
	maxWidth := contentWidth() * maxColShare

	for i := range cols {
		// A column is rigid when narrowing it destroys information rather than
		// reflowing it. Numbers and dates qualify by type; an explicit weight
		// qualifies because the caller has already decided. The third case is
		// the one the 200-row export found: "SO-2026-4100" has no space in it,
		// so a narrow column does not wrap it, it truncates it — and an order
		// number with its last two digits replaced by an ellipsis is not a
		// narrower cell, it is a wrong one.
		rigid[i] = cols[i].kind.Numeric() || cols[i].kind == format.KindDate || cols[i].weight > 0

		if cols[i].weight > 0 {
			// An explicit weight is a relative number, not a millimetre: 1 is
			// an even share of the measure, 3.2 is three and a bit shares. It
			// is converted here so it can be normalised against the measured
			// columns, and it goes through the same cap as they do — a weight
			// of 5 on a five-column table asks for the whole page, and a
			// column that takes the whole page takes it from the numbers.
			natural[i] = math.Min(cols[i].weight*contentWidth()/float64(len(cols)), maxWidth)
			continue
		}

		w := textWidth(header[i], theme.FontMedium, fontstyle.Normal, theme.TypeScale.Body)
		// Every row is measured, not a stride through them. An earlier version
		// sampled every k-th row to save work, and the 200-row export showed
		// why that is a false economy twice over: the saving is nothing next to
		// the per-cell wrapping that happens later anyway, and a stride lands
		// on a phase of whatever pattern the data has. In that fixture it
		// missed every "Marketplace" in the channel column, measured the column
		// against "Reseller", and clipped a quarter of its own rows.
		samples := make([]float64, 0, len(body))
		unbreakable, seen := 0, 0
		for j := range body {
			if i >= len(body[j]) {
				continue
			}
			value := body[j][i]
			samples = append(samples, textWidth(value, theme.FontBody, fontstyle.Normal, theme.TypeScale.Body))
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
		if i < len(totals) {
			w = max(w, textWidth(totals[i], theme.FontMedium, fontstyle.Normal, theme.TypeScale.Body))
		}
		w += 2 * cellPadX
		if rigid[i] {
			// One grid unit of slack. Widths are exact millimetres and columns
			// are integer units, so a column measured at 24.0mm can be handed
			// 23.2mm and clip the value it was measured from — which is how
			// "$2,400.00" became "$2,400.…" in a column with 0.8mm missing.
			// A flexible column does not need this: it is expected to reflow.
			w += colWidth(1)
		}
		natural[i] = math.Min(w, maxWidth)
	}

	weights := layout.Allocate(natural, rigid, contentWidth(), colWidth(minColUnits))
	units := layout.Distribute(weights, theme.GridCols, minColUnits)
	for i := range cols {
		cols[i].units = units[i]
	}
}

// rowHeightFor is the height of a row: the tallest cell in it, floored at the
// theme's row height so a table of one-line rows keeps its rhythm.
func rowHeightFor(cells []string, cols []tableColumn, family string) float64 {
	lh := lineHeight(theme.TypeScale.Body)
	lines := 1
	for i, c := range cells {
		if i >= len(cols) {
			break
		}
		n := len(wrapLines(c, family, fontstyle.Normal, theme.TypeScale.Body, colWidth(cols[i].units)-2*cellPadX))
		lines = max(lines, min(n, maxCellLines))
	}
	return max(theme.Page.TableRowHeight, float64(lines)*lh*bodyLeading+2*cellPadX*0.6)
}

func truncateRow(cells []string, cols []tableColumn) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if i >= len(cols) {
			out[i] = c
			continue
		}
		out[i] = fitText(c, theme.FontBody, fontstyle.Normal, theme.TypeScale.Body,
			colWidth(cols[i].units)-2*cellPadX, maxCellLines)
	}
	return out
}

func (r *renderer) emitTableHeader(header []string, cols []tableColumn, height float64) {
	style := &props.Cell{
		BackgroundColor: theme.ColorSurfaceSubtle.Props(),
		BorderColor:     theme.ColorBorder.Props(),
		BorderType:      border.Bottom,
		BorderThickness: theme.Page.Hairline,
	}
	cells := make([]core.Col, len(cols))
	for i, c := range cols {
		cells[i] = col.New(c.units).WithStyle(style).Add(
			text.New(header[i], props.Text{
				Family: theme.FontMedium,
				Size:   theme.TypeScale.Body,
				Color:  theme.ColorForeground.Props(),
				Align:  c.align,
				Left:   cellPadX,
				Right:  cellPadX,
				Top:    (height - lineHeight(theme.TypeScale.Body)) / 2,
			}),
		)
	}
	r.m.AddRow(height, cells...)
}

func (r *renderer) emitTableRow(cells []string, cols []tableColumn, height float64, banded, total bool) {
	var style *props.Cell
	family := theme.FontBody
	switch {
	case total:
		family = theme.FontMedium
		style = &props.Cell{
			BackgroundColor: theme.ColorSurfaceSubtle.Props(),
			BorderColor:     theme.ColorBorder.Props(),
			BorderType:      border.Top,
			BorderThickness: theme.Page.Hairline * 2,
		}
	case banded:
		// Zebra bands rather than rules between every row: a band survives a
		// photocopier, a 0.2mm rule does not.
		style = &props.Cell{BackgroundColor: theme.ColorSurfaceMuted.Props()}
	}

	lh := lineHeight(theme.TypeScale.Body)
	out := make([]core.Col, len(cols))
	for i, c := range cols {
		value := ""
		if i < len(cells) {
			value = cells[i]
		}
		// Multi-line cells start at the top of the row; single-line cells are
		// centred in it. Anything else makes a two-line cell look like it
		// belongs to the row beneath.
		top := (height - lh) / 2
		if len(wrapLines(value, family, fontstyle.Normal, theme.TypeScale.Body, colWidth(c.units)-2*cellPadX)) > 1 {
			top = cellPadX * 0.6
		}
		cell := col.New(c.units).Add(
			text.New(value, props.Text{
				Family:          family,
				Size:            theme.TypeScale.Body,
				Color:           theme.ColorForeground.Props(),
				Align:           c.align,
				Left:            cellPadX,
				Right:           cellPadX,
				Top:             top,
				VerticalPadding: lh * (bodyLeading - 1),
			}),
		)
		if style != nil {
			cell = cell.WithStyle(style)
		}
		out[i] = cell
	}
	r.m.AddRow(height, out...)
}
