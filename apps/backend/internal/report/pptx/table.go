package pptx

import (
	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

// Paging a table onto slides.
//
// The table *model* — every column width, every formatted cell, every measured
// row height — moved to internal/report/canvas in T-V1, because the video
// renderer needs the identical answers and two solvers would eventually give
// two. What is left here is the one decision that is genuinely the deck's: how
// the rows are distributed across slides, and which slide the total row lands
// on.

// tableModel is the shared model under the name this renderer has always used.
type tableModel = canvas.TableModel

// The alignment values the solver writes are OOXML's own — `l`, `ctr`, `r` —
// declared in drawing.go beside the rest of the OOXML vocabulary. canvas.Align*
// carries the same three strings; a test asserts they have not drifted apart,
// because a mismatch would silently left-align every currency column.

const (
	cellPadX     = canvas.CellPadX
	cellPadY     = canvas.CellPadY
	maxCellLines = canvas.MaxCellLines
)

func (b *builder) Table(title string, t *spec.Table) error {
	if t == nil || len(t.Columns) == 0 {
		return nil
	}
	m := surface.BuildTable(t, b.r.fmt)

	rows := m.RowsPerSurface()
	pages := chunk(m.Rows, rows)
	if len(pages) == 0 {
		pages = [][][]string{nil}
	}

	for i, page := range pages {
		part := *m
		part.Rows = page
		// The total row belongs on the last slide of the run, under the last
		// rows it totals. Repeating it on every continuation would state the
		// same sum three times against three different subsets of the data.
		if i < len(pages)-1 {
			part.Total = nil
		}
		b.slides = append(b.slides, slide{
			kind:      kindTable,
			title:     title,
			subtitle:  m.Caption,
			table:     &part,
			continued: i > 0,
		})
	}
	return nil
}
