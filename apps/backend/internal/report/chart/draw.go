package chart

import (
	"fmt"
	"math"

	"github.com/go-analyze/charts"
	"github.com/golang/freetype/truetype"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// draw dispatches a normalized plot onto the painter.
//
// The chart's own title is deliberately not drawn: the PDF renderer sets it in
// the document's type above the image, where it can wrap, where it is the same
// H2 as every other sub-heading, and where it is text a reader can select. A
// title baked into a bitmap is a title at a different size to the rest of the
// document, and T-R4 would have to bake a second one at slide scale.
func draw(p *charts.Painter, c *spec.Chart, pl plot, g geometry, opts Options) error {
	switch c.Type {
	case spec.ChartLine:
		return p.LineChart(lineOption(c, pl, g, opts, false))
	case spec.ChartSparkline:
		return p.LineChart(lineOption(c, pl, g, opts, true))
	case spec.ChartBar, spec.ChartGroupedBar, spec.ChartStackedBar:
		return p.BarChart(barOption(c, pl, g, opts))
	case spec.ChartPie:
		return p.PieChart(pieOption(pl, g))
	case spec.ChartDonut:
		return p.DoughnutChart(donutOption(pl, g, opts))
	default:
		return fmt.Errorf("chart: unknown type %q", c.Type)
	}
}

// axisFormatter is how a number on an axis or a value label gets written.
//
// Compact, and that is the whole point of it existing: an axis tick reading
// "3.863.405.700" is 14 characters of axis gutter for a number nobody reads
// digit by digit, and four of them stacked up push the plot into a third of
// the frame. "3,86 Miliar" says the same thing. The exact figures live in the
// table under the chart, formatted by the same package, so the two never
// disagree about what a rupiah looks like.
func axisFormatter(c *spec.Chart, opts Options) charts.ValueFormatter {
	o := opts.Format
	o.Compact = true
	o.Decimals = format.AutoDecimals
	kind := format.ParseKind(c.Fmt)
	return func(v float64) string {
		return format.Value(v, kind, o)
	}
}

// labelFontStyle is the type every label on a chart is set in: the theme's
// caption size, scaled to the resolution being drawn at.
//
// The face is named on every style rather than left to the painter's default.
// The painter's font only reaches text the painter draws directly; an axis
// label resolves its own font, and with none set it silently falls back to the
// library's Roboto — a chart in one typeface inside a document set in another,
// which is the exact drift the token package exists to stop.
func labelFontStyle(g geometry, c theme.Color) charts.FontStyle {
	return charts.FontStyle{
		FontSize:  g.ptPx(theme.TypeScale.Caption),
		FontColor: rgba(c),
		Font:      mustFont(),
	}
}

// mustFont is chartFont with the error dropped, for the option builders.
//
// Dropping it is safe by the time these run: Render calls chartFont before it
// reaches any of them and returns the error itself, so a nil here would mean
// the font failed to install between two statements. A nil face falls back to
// the library's own, which is a chart in the wrong typeface rather than no
// document at all.
func mustFont() *truetype.Font {
	face, _ := chartFont()
	return face
}

// denseAxisThreshold is the number of categories above which the library is
// asked to thin the axis labels.
//
// Below it, every label is drawn — a six-month chart that silently omits April
// is a chart a reader has to count. Above it they collide, and the alternative
// to thinning is rotating them, which costs a third of the plot height and is
// read by nobody.
const denseAxisThreshold = 12

// padding is the gutter between the plot and the edge of the image, in
// millimetres of printed page. It is small on every side because the chart is
// already inside a document with margins — a library's default padding would
// indent it a second time.
func padding(g geometry, mm float64) charts.Box {
	px := int(g.mmPx(mm))
	return charts.Box{Left: px, Top: px, Right: px, Bottom: px, IsSet: true}
}

func legendOption(pl plot, g geometry) charts.LegendOption {
	names := make([]string, 0, len(pl.series))
	for _, s := range pl.series {
		names = append(names, s.name)
	}
	return charts.LegendOption{
		SeriesNames: names,
		FontStyle:   labelFontStyle(g, theme.ColorForeground),
		Symbol:      charts.SymbolSquare,
	}
}

// wantsLegend reports whether a legend earns its space.
//
// Two conditions, both necessary. Fewer than two series and the legend is
// restating the title. A series with no name and the legend has a swatch
// against a blank, which sends the reader looking for a caption that does not
// exist.
func wantsLegend(pl plot) bool {
	if len(pl.series) < 2 {
		return false
	}
	for _, s := range pl.series {
		if s.name == "" {
			return false
		}
	}
	return true
}

// axisRange resolves the value axis bounds.
//
// The spec's own min and max win when it sets them. What this adds is two
// things the spec cannot know:
//
// A zero baseline, when fromZero is set. A bar encodes its value as a length,
// so an axis that starts at 390 million draws a 400-million bar at a twentieth
// the height of a 600-million one — the single most common way a correct number
// is used to say something false. Lines encode value as position and do not
// have this problem, which is why they keep the library's fitted range and get
// the resolution that comes with it.
//
// A range at all, when every finite value is the same number. The library then
// computes a zero-height range and prints the same tick three times against a
// line pinned to the middle of the frame. Anchoring at zero turns it back into
// a reading: one flat line, this far above nothing.
func axisRange(c *spec.Chart, pl plot, fromZero bool) (min, max *float64) {
	min, max = axisBound(c, true), axisBound(c, false)
	if min != nil || max != nil {
		return min, max
	}

	lo, hi, ok := pl.extent()
	if !ok {
		return nil, nil
	}
	if lo == hi {
		switch {
		case lo > 0:
			return charts.Ptr(0.0), nil
		case lo < 0:
			return nil, charts.Ptr(0.0)
		default:
			// A single zero has no scale of its own to be shown against.
			return charts.Ptr(-1.0), charts.Ptr(1.0)
		}
	}
	if fromZero {
		if lo > 0 {
			return charts.Ptr(0.0), nil
		}
		if hi < 0 {
			return nil, charts.Ptr(0.0)
		}
	}
	return nil, nil
}

// extent is the finite range of everything plotted.
func (p plot) extent() (min, max float64, ok bool) {
	min, max = math.Inf(1), math.Inf(-1)
	for _, s := range p.series {
		for _, v := range s.values {
			if !finite(v) {
				continue
			}
			ok = true
			min = math.Min(min, v)
			max = math.Max(max, v)
		}
	}
	return min, max, ok
}

func values(pl plot) [][]float64 {
	out := make([][]float64, len(pl.series))
	for i, s := range pl.series {
		out[i] = s.values
	}
	return out
}

func lineOption(c *spec.Chart, pl plot, g geometry, opts Options, spark bool) charts.LineChartOption {
	opt := charts.NewLineChartOptionWithData(values(pl))
	opt.Theme = palette()
	opt.ValueFormatter = axisFormatter(c, opts)
	opt.LineStrokeWidth = g.mmPx(0.45)

	// A line through one point draws no segment at all, so the point itself has
	// to carry the chart. SymbolDot rather than SymbolCircle: the library
	// renders a circle as a ring filled with the background, which at print
	// scale downsamples to a faint outline, and the acceptance criterion here
	// is that a single-point series is visible.
	//
	// Above one point the symbols come off. At forty points they are the
	// densest ink on the page and they say nothing the line does not.
	opt.Symbol = charts.Symbol{Shape: charts.SymbolNone}
	if pl.single {
		opt.Symbol = charts.Symbol{Shape: charts.SymbolDot, Size: g.mmPx(1.0)}
		// Without a boundary gap a lone point is placed at the left edge of the
		// plot, half of it outside the frame. The gap centres it in its slot.
		opt.XAxis.BoundaryGap = charts.Ptr(true)
	}

	if spark {
		// A sparkline is the shape of a series and nothing else: no axes, no
		// ticks, no legend, no frame. It is read at the size of a line of text
		// inside a KPI card, where any of those would be noise at best and
		// illegible at worst.
		opt.XAxis.Show = charts.Ptr(false)
		opt.YAxis = []charts.YAxisOption{{Show: charts.Ptr(false), SpineLineShow: charts.Ptr(false)}}
		opt.Padding = padding(g, 0.5)
		opt.FillArea = charts.Ptr(true)
		opt.FillOpacity = 40
		opt.LineStrokeWidth = g.mmPx(0.35)
		return opt
	}

	opt.Padding = padding(g, 2)
	opt.XAxis.Labels = pl.labels
	opt.XAxis.LabelFontStyle = labelFontStyle(g, theme.ColorMuted)
	// Negative because the library's default label count assumes a screen and
	// collides at print widths — and because the alternative to fewer labels on
	// a dense axis is rotated labels, which cost a third of the plot height and
	// are read by nobody.
	if len(pl.labels) > denseAxisThreshold {
		opt.XAxis.LabelCountAdjustment = -1
	}

	min, max := axisRange(c, pl, false)
	opt.YAxis = []charts.YAxisOption{{
		LabelFontStyle: labelFontStyle(g, theme.ColorMuted),
		SplitLineShow:  charts.Ptr(true),
		ValueFormatter: axisFormatter(c, opts),
		Min:            min,
		Max:            max,
		Title:          axisTitle(c),
		TitleFontStyle: labelFontStyle(g, theme.ColorMuted),
	}}
	if wantsLegend(pl) {
		opt.Legend = legendOption(pl, g)
	}
	if pl.single {
		for i := range opt.SeriesList {
			opt.SeriesList[i].Label = valueLabel(c, g, opts)
		}
	}
	return opt
}

// valueLabel writes a point's value beside it. Reserved for the single-point
// case: one number on a chart is the reading, and the axis it would otherwise
// be read off is a scale with one mark on it.
func valueLabel(c *spec.Chart, g geometry, opts Options) charts.SeriesLabel {
	return charts.SeriesLabel{
		Show:           charts.Ptr(true),
		FontStyle:      labelFontStyle(g, theme.ColorForeground),
		ValueFormatter: axisFormatter(c, opts),
	}
}

func barOption(c *spec.Chart, pl plot, g geometry, opts Options) charts.BarChartOption {
	opt := charts.NewBarChartOptionWithData(values(pl))
	opt.Theme = palette()
	opt.ValueFormatter = axisFormatter(c, opts)
	opt.Padding = padding(g, 2)
	opt.CategoryAxis = charts.CategoryAxisOption{
		Labels:         pl.labels,
		LabelFontStyle: labelFontStyle(g, theme.ColorMuted),
	}
	if len(pl.labels) > denseAxisThreshold {
		opt.CategoryAxis.LabelCountAdjustment = -1
	}
	min, max := axisRange(c, pl, true)
	opt.ValueAxis = []charts.ValueAxisOption{{
		LabelFontStyle: labelFontStyle(g, theme.ColorMuted),
		SplitLineShow:  charts.Ptr(true),
		ValueFormatter: axisFormatter(c, opts),
		Min:            min,
		Max:            max,
		Title:          axisTitle(c),
		TitleFontStyle: labelFontStyle(g, theme.ColorMuted),
	}}
	if c.Type == spec.ChartStackedBar {
		opt.StackSeries = charts.Ptr(true)
	}
	if wantsLegend(pl) {
		opt.Legend = legendOption(pl, g)
	}

	// One bar is a number, not a chart, so it gets the number written on it.
	// Above one the value labels come off and the axis does the work — a bar
	// chart with a figure over every bar is a table drawn slowly.
	if pl.single {
		opt.SeriesLabelPosition = charts.PositionTop
		for i := range opt.SeriesList {
			opt.SeriesList[i].Label = valueLabel(c, g, opts)
		}
	}
	return opt
}

// shareLabel writes a slice's share of the whole and nothing else.
//
// The name is in the legend already. The library's default slice label is
// "name: 15.75%", which on a four-slice pie is four names orbiting the circle,
// the longest of them running off the edge of the frame — the pie in the first
// T-R3 contact sheet had "Marketplace" clipped to "rketplace". Percentages are
// short, they are the same length whatever the category is called, and they are
// what a reader is looking at a pie for.
func shareLabel(pl plot, g geometry) charts.SeriesLabel {
	total := 0.0
	for _, v := range pl.series[0].values {
		if finite(v) {
			total += math.Abs(v)
		}
	}
	return charts.SeriesLabel{
		Show:      charts.Ptr(true),
		FontStyle: labelFontStyle(g, theme.ColorForeground),
		LabelFormatter: func(_ int, _ string, val float64) (string, *charts.LabelStyle) {
			if total == 0 {
				return "", nil
			}
			return fmt.Sprintf("%.0f%%", val/total*100), nil
		},
	}
}

func sliceLegend(pl plot, g geometry) charts.LegendOption {
	return charts.LegendOption{
		SeriesNames: pl.labels,
		FontStyle:   labelFontStyle(g, theme.ColorForeground),
		Symbol:      charts.SymbolSquare,
	}
}

func pieOption(pl plot, g geometry) charts.PieChartOption {
	opt := charts.NewPieChartOptionWithData(pl.series[0].values)
	opt.Theme = palette()
	opt.Padding = padding(g, 2)
	// The gap is what separates two adjacent slices once they print as the same
	// grey — the greyscale half of the palette gate assumes it is there.
	opt.SegmentGap = g.mmPx(0.4)
	opt.Legend = sliceLegend(pl, g)

	label := shareLabel(pl, g)
	for i := range opt.SeriesList {
		if i < len(pl.labels) {
			opt.SeriesList[i].Name = pl.labels[i]
		}
		opt.SeriesList[i].Label = label
	}
	return opt
}

func donutOption(pl plot, g geometry, opts Options) charts.DoughnutChartOption {
	opt := charts.NewDoughnutChartOptionWithData(pl.series[0].values)
	opt.Theme = palette()
	opt.Padding = padding(g, 2)
	opt.SegmentGap = g.mmPx(0.4)
	opt.Legend = sliceLegend(pl, g)

	// The hole is what a donut is for: it holds the total, which is the number
	// a reader wants before they want any share of it.
	opt.CenterValues = "sum"
	opt.CenterValuesFontStyle = charts.FontStyle{
		FontSize:  g.ptPx(theme.TypeScale.Body),
		FontColor: rgba(theme.ColorForeground),
		Font:      mustFont(),
	}
	sum := opts.Format
	sum.Compact = true
	sum.Decimals = format.AutoDecimals
	opt.ValueFormatter = func(v float64) string { return format.Value(v, format.KindNumber, sum) }

	label := shareLabel(pl, g)
	for i := range opt.SeriesList {
		if i < len(pl.labels) {
			opt.SeriesList[i].Name = pl.labels[i]
		}
		opt.SeriesList[i].Label = label
	}
	return opt
}

func axisBound(c *spec.Chart, min bool) *float64 {
	if c.YAxis == nil {
		return nil
	}
	if min {
		return c.YAxis.Min
	}
	return c.YAxis.Max
}

func axisTitle(c *spec.Chart) string {
	if c.YAxis == nil {
		return ""
	}
	return c.YAxis.Label
}
