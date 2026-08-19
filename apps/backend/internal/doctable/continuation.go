package doctable

import (
	"strconv"
	"strings"
)

// PageBox is where one page's part of a table sat, in PDF points.
//
// A table that spans three pages has three of these, and the review surface
// (T-P7) needs every one of them: a reviewer checking a joined table against
// the document has to be shown the three rectangles it was joined from, not the
// first one three times.
type PageBox struct {
	Page int       `json:"page"`
	BBox []float64 `json:"bbox,omitempty"`
}

// continues reports whether `next` is the same table as `prev`, carried onto
// the following page.
//
// Three things have to agree, and all three are cheap facts rather than
// inferences: the column count, the resolved header, and the left edge of the
// grid. **No model, and no similarity score.** A wrong join is one table
// containing another table's rows — every figure in it is real and every
// aggregate over it is nonsense — so the rule is conservative on purpose: when
// it is unsure it leaves two tables, and two tables that should have been one
// is a thing a reviewer can see and fix.
func continues(prev, next Table) bool {
	if len(prev.Columns) == 0 || len(prev.Columns) != len(next.Columns) {
		return false
	}
	if next.FirstPage != prev.LastPage+1 {
		return false
	}
	for i := range prev.Columns {
		if !sameHeader(prev.Columns[i].Header, next.Columns[i].Header) {
			return false
		}
	}
	return sameLeftEdge(prev, next)
}

// sameHeader compares two header strings the way a reader would: case and
// spacing are noise, everything else is not.
//
// A blank header on the continuation is **not** a match. A table whose second
// page repeats no header at all is a real shape, and this rule leaves it
// unjoined — the alternative is joining any two same-width grids that happen to
// follow each other, which is how a price list acquires the rows of the
// delivery schedule printed after it.
func sameHeader(a, b string) bool {
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.ToLower(s)), " ")
	}
	na, nb := norm(a), norm(b)
	return na != "" && na == nb
}

// sameLeftEdge compares where the two grids start on their pages. A continued
// table is printed by the same layout at the same margin; a different table
// that happens to share a header is usually indented differently, and when it
// is not, the header check has already done the work.
func sameLeftEdge(prev, next Table) bool {
	last := lastBox(prev)
	first := firstBox(next)
	if len(last) != 4 || len(first) != 4 {
		// No rectangles — a parser that did not report them, or a fixture. The
		// header and column count agreeing is enough evidence to join on; this
		// check is the third of three, not the load-bearing one.
		return true
	}
	const tolerance = 6.0 // points; about a millimetre and a half
	return absf(last[0]-first[0]) <= tolerance
}

func lastBox(t Table) []float64 {
	if len(t.Boxes) == 0 {
		return nil
	}
	return t.Boxes[len(t.Boxes)-1].BBox
}

func firstBox(t Table) []float64 {
	if len(t.Boxes) == 0 {
		return nil
	}
	return t.Boxes[0].BBox
}

// join appends the continuation's rows to the table, in page order.
//
// The continuation's own columns are dropped: they are the same header read a
// second time, and keeping them would mean deciding which page's spelling wins.
// Its title is dropped for the same reason. What is kept is every row, every
// total row, and the rectangle — because the join has to be visible in the
// review surface, and a joined table that cannot show its second page is a
// joined table nobody can check.
func join(prev, next Table) Table {
	prev.Rows = append(prev.Rows, next.Rows...)
	prev.Totals = append(prev.Totals, next.Totals...)
	prev.Boxes = append(prev.Boxes, next.Boxes...)
	prev.LastPage = next.LastPage
	prev.Notes = append(prev.Notes,
		"joined the continuation on page "+strconv.Itoa(next.FirstPage)+
			" ("+strconv.Itoa(len(next.Rows))+" rows, the header repeated)")
	prev.Notes = append(prev.Notes, next.Notes...)
	return prev
}
