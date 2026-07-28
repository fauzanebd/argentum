// Package layout is the column-width solver, shared by the PDF table pager and
// the deck's table slides.
//
// It is arithmetic over widths and holds no opinion about the medium: the PDF
// asks it for integer grid units and the deck asks it for millimetres it will
// convert to EMU. What both get is the same proportioning, which is the point —
// a table whose columns are balanced one way in the report and another way in
// the deck attached to it is the same self-disagreement two chart renderers
// would produce, reached through typography instead of through data.
package layout

import "sort"

// Allocate resolves natural widths against the available measure.
//
// Columns are not equally compressible, so they are not scaled equally. "Rp
// 121.000" and "1 Januari 2026" either fit on one line or wrap into nonsense; a
// customer name gives up a word and reads fine. Rigid columns are therefore
// paid first at the width they asked for, and the flexible ones divide what is
// left in proportion to what they wanted.
//
// If honouring the rigid columns would leave the flexible ones below the
// readable minimum, nothing is rigid — the table is over-subscribed and
// proportional shrinking is the only honest answer.
//
// Proportional scaling of everything, which is what the first version of this
// did, wraps every currency cell in an eight-column table onto two lines while
// a text column keeps space it did not need.
func Allocate(natural []float64, rigid []bool, available, minWidth float64) []float64 {
	var rigidSum, flexSum float64
	flexCount := 0
	for i, w := range natural {
		if i < len(rigid) && rigid[i] {
			rigidSum += w
			continue
		}
		flexSum += w
		flexCount++
	}
	if flexCount == 0 || rigidSum+float64(flexCount)*minWidth > available {
		return natural
	}

	out := make([]float64, len(natural))
	remaining := available - rigidSum
	for i, w := range natural {
		if i < len(rigid) && rigid[i] {
			out[i] = w
			continue
		}
		out[i] = remaining * w / flexSum
	}
	return out
}

// Distribute turns arbitrary positive weights into integers that sum to total,
// with every entry at least minUnits.
//
// Largest-remainder rather than plain rounding: rounding each column
// independently leaves the row a unit or two short of the grid, and maroto
// silently renders a short row as a gap on the right-hand side.
//
// The minimum is enforced afterwards, by taking units from the widest columns,
// not by reserving minUnits per column up front and dividing what is left. The
// reserving version is subtly wrong in a way that is easy to miss and shows up
// in every wide table: with eight columns it hands out 48 of 120 units flat and
// then splits the other 72 in proportion to full widths, so a column asking for
// 15% of the table gets 6 + 10.8 units instead of 18. Every wide column comes
// out narrower than it asked for and every narrow one wider — which is how an
// order number ends up truncated beside a "Qty" column with room to spare.
func Distribute(weights []float64, total, minUnits int) []int {
	n := len(weights)
	out := make([]int, n)
	if n == 0 {
		return out
	}
	// Too many columns for the minimum to hold: shrink the minimum rather than
	// the table. The tool description keeps tables under eight columns; this is
	// the guard for when it is ignored.
	if n*minUnits > total {
		minUnits = max(1, total/n)
	}

	sum := 0.0
	for _, w := range weights {
		if w > 0 {
			sum += w
		}
	}
	if sum <= 0 {
		for i := range out {
			out[i] = total / n
		}
		out[n-1] += total - (total/n)*n
		return out
	}

	type frac struct {
		i int
		f float64
	}
	fracs := make([]frac, 0, n)
	used := 0
	for i, w := range weights {
		if w < 0 {
			w = 0
		}
		exact := w / sum * float64(total)
		whole := int(exact)
		out[i] = whole
		used += whole
		fracs = append(fracs, frac{i, exact - float64(whole)})
	}
	sort.SliceStable(fracs, func(a, b int) bool { return fracs[a].f > fracs[b].f })
	for k := 0; used < total; k++ {
		out[fracs[k%n].i]++
		used++
	}

	enforceMinimum(out, minUnits)
	return out
}

// enforceMinimum raises every column to minUnits, paying for it out of the
// widest column each time. It terminates because the guard above guarantees
// n*minUnits <= sum(out), so there is always a column above the minimum to take
// from while one sits below it.
func enforceMinimum(units []int, minUnits int) {
	for {
		short, widest := -1, 0
		for i, u := range units {
			if u < minUnits && (short == -1 || u < units[short]) {
				short = i
			}
			if units[i] > units[widest] {
				widest = i
			}
		}
		if short == -1 || units[widest] <= minUnits {
			return
		}
		units[short]++
		units[widest]--
	}
}

// Scale turns weights into widths summing to exactly total, for a caller
// measuring in a continuous unit rather than in grid cells. It is Distribute's
// counterpart for the deck, where a column is an EMU width and there is no grid
// to round to.
func Scale(weights []float64, total, minWidth float64) []float64 {
	n := len(weights)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	if float64(n)*minWidth > total {
		minWidth = total / float64(n)
	}

	sum := 0.0
	for _, w := range weights {
		if w > 0 {
			sum += w
		}
	}
	if sum <= 0 {
		for i := range out {
			out[i] = total / float64(n)
		}
		return out
	}
	for i, w := range weights {
		if w < 0 {
			w = 0
		}
		out[i] = max(minWidth, w/sum*total)
	}

	// Raising the narrow columns to the floor has overspent the measure; take
	// it back from the columns that are above the floor, in proportion to how
	// far above it they are, so the table still ends flush with the margin.
	spent := 0.0
	for _, w := range out {
		spent += w
	}
	if over := spent - total; over > 0 {
		var slack float64
		for _, w := range out {
			slack += w - minWidth
		}
		if slack > 0 {
			for i := range out {
				out[i] -= over * (out[i] - minWidth) / slack
			}
		}
	}
	return out
}
