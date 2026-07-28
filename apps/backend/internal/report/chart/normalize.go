package chart

import (
	"math"
	"sort"

	"github.com/fauzanebd/argentum/internal/report/spec"
)

// Caps. Both are defaults in the ticket's sense — a caller can lower them
// through Options — and both are about legibility rather than about cost.
const (
	// MaxSeries is where a categorical palette runs out. The palette has eight
	// rungs on its L* ladder (see packages/design-tokens/tokens.json); a ninth
	// series would wrap onto the first one's colour, and two series drawn in
	// the same red is worse than one series left out and said so.
	MaxSeries = 8

	// MaxCategories is where bars stop being bars. Forty across a 174mm measure
	// is about 3mm of ink each, which is the last width at which a reader can
	// still tell two adjacent bars apart on paper.
	MaxCategories = 40
)

// plot is a chart reduced to what the drawing code needs: labels, series, and
// the sentences the caption owes the reader about what was left out.
type plot struct {
	labels []string
	series []series
	notes  []string

	// empty is a chart with nothing finite in it. single is a chart with
	// exactly one point per series — a line through one point draws nothing,
	// so it has to be drawn as something else.
	empty  bool
	single bool
}

type series struct {
	name   string
	values []float64
}

// normalize applies the caps and finds the two states that are not a chart.
//
// It never reorders: a chart's categories arrive in the order the query
// returned them, which for the common case is chronological, and sorting a
// month series by revenue produces a plot that is technically accurate and
// tells the reader something false about time.
func normalize(c *spec.Chart, lab labels, maxSeries, maxCategories int) plot {
	p := plot{labels: append([]string(nil), c.Labels...)}
	for _, s := range c.Series {
		p.series = append(p.series, series{name: s.Name, values: append([]float64(nil), s.Values...)})
	}

	// A pie is one series by validation, but a caller assembling a spec in Go
	// is not validated, and drawing the first of several silently is how a
	// second series disappears without anybody noticing.
	if spec.SingleSeries(c.Type) && len(p.series) > 1 {
		p.series = p.series[:1]
	}

	p.padToLabels()
	p.dropEmptySeries()

	if len(p.series) == 0 || !p.hasFinite() {
		p.empty = true
		return p
	}

	if maxSeries > 0 && len(p.series) > maxSeries {
		p.capSeries(maxSeries, spec.ChartStackedBar == c.Type, lab)
	}
	// Only categorical types get the category cap. On a line chart the x-axis
	// is a sequence, so folding the twelve smallest points into an "Other"
	// bucket would put a made-up point on a real timeline. A dense line is
	// legible in a way a dense bar chart is not — the fix there is fewer axis
	// labels, which the drawing code asks the library for, not fewer points.
	if spec.Categorical(c.Type) && maxCategories > 0 && len(p.labels) > maxCategories {
		p.capCategories(maxCategories, lab)
	}

	p.single = p.longest() == 1
	return p
}

// padToLabels squares the matrix. Validation rejects a mismatch from the model,
// but Render is exported and a Go caller can hand it anything; a series shorter
// than its labels must not silently shift its points left.
func (p *plot) padToLabels() {
	width := len(p.labels)
	for i := range p.series {
		width = max(width, len(p.series[i].values))
	}
	for len(p.labels) < width {
		p.labels = append(p.labels, "")
	}
	for i := range p.series {
		for len(p.series[i].values) < width {
			p.series[i].values = append(p.series[i].values, math.NaN())
		}
	}
}

// dropEmptySeries removes series with no finite value at all. A legend entry
// for a line that was never drawn is a reader looking for something that is
// not there.
func (p *plot) dropEmptySeries() {
	kept := p.series[:0]
	for _, s := range p.series {
		if finiteCount(s.values) > 0 {
			kept = append(kept, s)
		}
	}
	p.series = kept
}

func (p *plot) hasFinite() bool {
	for _, s := range p.series {
		if finiteCount(s.values) > 0 {
			return true
		}
	}
	return false
}

func (p *plot) longest() int {
	n := 0
	for _, s := range p.series {
		n = max(n, len(s.values))
	}
	return n
}

// capSeries keeps the n largest by total magnitude, in their original order.
//
// Whether the remainder becomes an "Other" series or simply goes depends on
// whether adding them up means anything. In a stack the bar's height is
// already a sum, so an "Other" band is the same arithmetic the chart is doing
// anyway. In a grouped bar or a line chart it is not: the sum of "Direct" and
// "Referral" is not a fifth line anybody asked for, and drawing one invents a
// series the data does not contain.
func (p *plot) capSeries(n int, stacked bool, lab labels) {
	total := len(p.series)

	// In a stack one rung is spent on the bucket, so one fewer real series
	// survives — the chart still has n bands.
	shown := n
	if stacked {
		shown = n - 1
	}

	keep := make([]bool, total)
	for _, i := range rankByMagnitude(p.series)[:shown] {
		keep[i] = true
	}

	kept := make([]series, 0, n)
	rest := make([]float64, p.longest())
	for i, s := range p.series {
		if keep[i] {
			kept = append(kept, s)
			continue
		}
		for j, v := range s.values {
			if finite(v) {
				rest[j] += v
			}
		}
	}

	if stacked {
		p.series = append(kept, series{name: lab.other, values: rest})
		p.notes = append(p.notes, lab.seriesBucketed(shown, total))
		return
	}
	p.series = kept
	p.notes = append(p.notes, lab.seriesDropped(shown, total))
}

// capCategories keeps the n-1 largest categories and folds the rest into one
// "Other" bucket, so the chart still adds up to the same total.
func (p *plot) capCategories(n int, lab labels) {
	total := len(p.labels)
	shown := n - 1

	// Rank by the total across every series, so a category that is small in one
	// series and large in another survives.
	magnitude := make([]float64, total)
	for _, s := range p.series {
		for i, v := range s.values {
			if finite(v) {
				magnitude[i] += math.Abs(v)
			}
		}
	}
	idx := make([]int, total)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return magnitude[idx[a]] > magnitude[idx[b]] })

	keep := make([]bool, total)
	for _, i := range idx[:shown] {
		keep[i] = true
	}

	labels := make([]string, 0, n)
	for i, l := range p.labels {
		if keep[i] {
			labels = append(labels, l)
		}
	}
	labels = append(labels, lab.other)

	for si := range p.series {
		values := make([]float64, 0, n)
		other := 0.0
		for i, v := range p.series[si].values {
			if keep[i] {
				values = append(values, v)
			} else if finite(v) {
				other += v
			}
		}
		p.series[si].values = append(values, other)
	}
	p.labels = labels
	p.notes = append(p.notes, lab.categoriesBucketed(shown, total))
}

// rankByMagnitude returns series indices ordered largest first, ties broken by
// original position so the result does not depend on the sort's internals.
func rankByMagnitude(list []series) []int {
	idx := make([]int, len(list))
	weight := make([]float64, len(list))
	for i, s := range list {
		idx[i] = i
		for _, v := range s.values {
			if finite(v) {
				weight[i] += math.Abs(v)
			}
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return weight[idx[a]] > weight[idx[b]] })
	return idx
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func finiteCount(values []float64) int {
	n := 0
	for _, v := range values {
		if finite(v) {
			n++
		}
	}
	return n
}
