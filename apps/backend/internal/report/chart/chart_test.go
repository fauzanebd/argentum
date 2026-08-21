package chart

import (
	"bytes"
	"image"
	"image/color"
	ximg "image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/freetype/truetype"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// idOpts is a deliberately small chart in the Indonesian convention.
//
// Small because every render here is supersampled at 200 DPI, and a
// full-measure 174mm chart is millions of pixels of rasteriser per call — which
// under `go test -race` is most of a minute across this file. 90mm still
// exercises every layout decision the renderer makes; it just does it on a
// quarter of the pixels. The contact sheet, which a human looks at, is drawn at
// its real size.
func idOpts() Options {
	return Options{
		WidthMM:  90,
		HeightMM: 50,
		Format: format.Options{
			Locale:   format.LocaleID,
			Currency: "IDR",
			Decimals: format.AutoDecimals,
		},
	}
}

// fixtures are one chart per supported type, all off the same shape of data, so
// the contact sheet compares the types rather than the numbers.
func fixtures() []*spec.Chart {
	months := []string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun"}
	direct := []float64{412_000_000, 448_000_000, 391_000_000, 520_000_000, 604_000_000, 588_000_000}
	partner := []float64{210_000_000, 233_000_000, 268_000_000, 251_000_000, 305_000_000, 342_000_000}
	channel := []string{"Direct", "Partner", "Marketplace", "Reseller"}

	return []*spec.Chart{
		{Type: spec.ChartLine, Title: "Revenue by month", Labels: months, Fmt: "currency",
			Series: []spec.Series{{Name: "Direct", Values: direct}, {Name: "Partner", Values: partner}}},
		{Type: spec.ChartBar, Title: "Revenue by month", Labels: months, Fmt: "currency",
			Series: []spec.Series{{Name: "Direct", Values: direct}}},
		{Type: spec.ChartGroupedBar, Title: "Direct vs partner", Labels: months, Fmt: "currency",
			Series: []spec.Series{{Name: "Direct", Values: direct}, {Name: "Partner", Values: partner}}},
		{Type: spec.ChartStackedBar, Title: "Total revenue", Labels: months, Fmt: "currency",
			Series: []spec.Series{{Name: "Direct", Values: direct}, {Name: "Partner", Values: partner}}},
		{Type: spec.ChartPie, Title: "Share by channel", Labels: channel, Fmt: "currency",
			Series: []spec.Series{{Values: []float64{604_000_000, 305_000_000, 188_000_000, 96_000_000}}}},
		{Type: spec.ChartDonut, Title: "Share by channel", Labels: channel, Fmt: "currency",
			Series: []spec.Series{{Values: []float64{604_000_000, 305_000_000, 188_000_000, 96_000_000}}}},
		{Type: spec.ChartSparkline, Title: "Revenue trend", Fmt: "currency",
			Series: []spec.Series{{Values: direct}}},
	}
}

// decode reads a rendered PNG back as NRGBA.
//
// Always NRGBA, never whatever png.Decode happened to produce, because every
// assertion below walks the pixels: going through image.Image.At is an
// interface call and a colour conversion per pixel, which on a multi-megapixel
// contact sheet under -race is most of this file's runtime.
func decode(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	out := image.NewNRGBA(img.Bounds())
	ximg.Draw(out, out.Bounds(), img, img.Bounds().Min, ximg.Src)
	return out
}

// inkFraction is the share of pixels that are not the chart's background.
//
// It is the only assertion available about a bitmap short of a golden file, and
// a golden PNG would fail on any freetype or resampler upgrade while telling
// nobody anything about whether the chart is right. What this catches is the
// failure that actually happens: a chart that renders an empty frame because a
// series was dropped, an axis collapsed, or a symbol was never drawn.
func inkFraction(img *image.NRGBA) float64 {
	bg := nrgba(theme.ColorSurface)
	ink, total := 0, 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		total++
		if absDiff(img.Pix[i], bg.R)+absDiff(img.Pix[i+1], bg.G)+absDiff(img.Pix[i+2], bg.B) > 12 {
			ink++
		}
	}
	return float64(ink) / float64(total)
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a) - int(b)
	}
	return int(b) - int(a)
}

func TestRendersEveryType(t *testing.T) {
	for _, c := range fixtures() {
		t.Run(c.Type, func(t *testing.T) {
			opts := idOpts()
			if c.Type == spec.ChartSparkline {
				opts.HeightMM = 14
			}
			res, err := Render(c, opts)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if res.Empty {
				t.Fatal("a chart with data reported itself empty")
			}
			img := decode(t, res.PNG)
			if got := img.Bounds().Dx(); got != res.WidthPx {
				t.Errorf("png width %d, Result says %d", got, res.WidthPx)
			}
			if got := img.Bounds().Dy(); got != res.HeightPx {
				t.Errorf("png height %d, Result says %d", got, res.HeightPx)
			}
			// 1% is far below what any real chart draws and far above what an
			// empty frame does, which is the gap this is measuring.
			if ink := inkFraction(img); ink < 0.01 {
				t.Errorf("chart is %0.4f ink — effectively blank", ink)
			}
		})
	}
}

// TestSeriesUseTheTokenPalette is what stops the chart drifting away from the
// document it sits in. The library ships its own colours and its own font; if
// either ever comes back, the brand red stops appearing in the plot.
func TestSeriesUseTheTokenPalette(t *testing.T) {
	c := fixtures()[2] // grouped_bar — two flat-filled series, easiest to count
	res, err := Render(c, idOpts())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	img := decode(t, res.PNG)

	for i, want := range []theme.Color{theme.ChartPalette[0], theme.ChartPalette[1]} {
		if n := countNear(img, want, 6); n < 100 {
			t.Errorf("series %d colour %s appears in %d pixels; expected the bars to be drawn in it",
				i+1, want.Hex(), n)
		}
	}
}

func countNear(img *image.NRGBA, want theme.Color, tolerance int) int {
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if absDiff(img.Pix[i], want.R) <= tolerance &&
			absDiff(img.Pix[i+1], want.G) <= tolerance &&
			absDiff(img.Pix[i+2], want.B) <= tolerance {
			n++
		}
	}
	return n
}

// TestDeterministic is the acceptance criterion "same input → same PNG bytes",
// and it is also what a golden-PDF test in the pdf package depends on: a chart
// that re-encodes differently every run makes the whole document
// irreproducible.
func TestDeterministic(t *testing.T) {
	for _, c := range fixtures() {
		t.Run(c.Type, func(t *testing.T) {
			first, err := Render(c, idOpts())
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			second, err := Render(c, idOpts())
			if err != nil {
				t.Fatalf("re-render: %v", err)
			}
			if !bytes.Equal(first.PNG, second.PNG) {
				t.Errorf("two renders of the same chart differ: %d vs %d bytes",
					len(first.PNG), len(second.PNG))
			}
		})
	}
}

func TestNoDataRendersItsOwnState(t *testing.T) {
	c := &spec.Chart{
		Type:   spec.ChartLine,
		Labels: []string{"Jan", "Feb", "Mar"},
		Series: []spec.Series{{Name: "Revenue", Values: []float64{
			math.NaN(), math.NaN(), math.NaN(),
		}}},
	}
	res, err := Render(c, idOpts())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !res.Empty {
		t.Fatal("a chart with no finite values did not report itself empty")
	}
	img := decode(t, res.PNG)
	// The panel is a filled rectangle with a border and a line of text, so it
	// is nearly all ink by this measure. A blank canvas would be ~0.
	if ink := inkFraction(img); ink < 0.5 {
		t.Errorf("no-data state is %0.4f ink; expected a filled panel with a message", ink)
	}
}

func TestSinglePointIsDrawn(t *testing.T) {
	for _, chartType := range []string{spec.ChartLine, spec.ChartBar, spec.ChartSparkline} {
		t.Run(chartType, func(t *testing.T) {
			c := &spec.Chart{
				Type:   chartType,
				Labels: []string{"Jun"},
				Fmt:    "currency",
				Series: []spec.Series{{Name: "Revenue", Values: []float64{588_000_000}}},
			}
			if chartType == spec.ChartSparkline {
				c.Labels = nil
			}
			res, err := Render(c, idOpts())
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			img := decode(t, res.PNG)
			// A line through one point draws no segment. If the explicit
			// single-point handling is removed, a line chart here falls to
			// axis furniture only, which is well under a percent of the frame.
			if n := countNear(img, theme.ChartPalette[0], 24); n < 30 {
				t.Errorf("single point drawn in only %d series-coloured pixels; the point is invisible", n)
			}
		})
	}
}

func TestSeriesCapDropsAndSays(t *testing.T) {
	c := &spec.Chart{Type: spec.ChartLine, Labels: []string{"Jan", "Feb"}}
	for i := range 11 {
		c.Series = append(c.Series, spec.Series{
			Name:   string(rune('A' + i)),
			Values: []float64{float64(i + 1), float64(i + 1)},
		})
	}
	p := normalize(c, labelsFor(format.LocaleEN), MaxSeries, MaxCategories)
	if len(p.series) != MaxSeries {
		t.Fatalf("kept %d series, cap is %d", len(p.series), MaxSeries)
	}
	// The largest by magnitude survive, and the smallest three go.
	if p.series[0].name != "D" {
		t.Errorf("kept series start at %q; expected the three smallest (A, B, C) to be dropped", p.series[0].name)
	}
	note := joinNotes(p.notes)
	if !strings.Contains(note, "8") || !strings.Contains(note, "11") {
		t.Errorf("note %q does not say how many of how many are shown", note)
	}
}

func TestStackedBarBucketsTheRemainder(t *testing.T) {
	c := &spec.Chart{Type: spec.ChartStackedBar, Labels: []string{"Jan"}}
	for i := range 11 {
		c.Series = append(c.Series, spec.Series{
			Name:   string(rune('A' + i)),
			Values: []float64{float64(i + 1)},
		})
	}
	lab := labelsFor(format.LocaleEN)
	p := normalize(c, lab, MaxSeries, MaxCategories)

	if len(p.series) != MaxSeries {
		t.Fatalf("stack has %d bands, cap is %d", len(p.series), MaxSeries)
	}
	last := p.series[len(p.series)-1]
	if last.name != lab.other {
		t.Fatalf("last band is %q, expected the %q bucket", last.name, lab.other)
	}
	// The stack still adds up: A..D are 1+2+3+4.
	if last.values[0] != 10 {
		t.Errorf("Other band is %v, expected the dropped series summed to 10", last.values[0])
	}
}

func TestCategoryCapBucketsOnlyCategoricalTypes(t *testing.T) {
	labels := make([]string, 60)
	values := make([]float64, 60)
	for i := range labels {
		labels[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
		values[i] = float64(i + 1)
	}

	bar := &spec.Chart{Type: spec.ChartBar, Labels: labels,
		Series: []spec.Series{{Name: "n", Values: values}}}
	p := normalize(bar, labelsFor(format.LocaleEN), MaxSeries, MaxCategories)
	if len(p.labels) != MaxCategories {
		t.Errorf("bar kept %d categories, cap is %d", len(p.labels), MaxCategories)
	}
	if p.labels[len(p.labels)-1] != "Other" {
		t.Errorf("last bar category is %q, expected Other", p.labels[len(p.labels)-1])
	}
	// Nothing is lost: 60 consecutive integers sum to 1830.
	total := 0.0
	for _, v := range p.series[0].values {
		total += v
	}
	if total != 1830 {
		t.Errorf("bucketed values total %v, expected the original 1830", total)
	}

	// A line's x-axis is a sequence. Folding the smallest points into a bucket
	// would put an invented point on a real timeline, so the cap does not apply.
	line := &spec.Chart{Type: spec.ChartLine, Labels: labels,
		Series: []spec.Series{{Name: "n", Values: values}}}
	lp := normalize(line, labelsFor(format.LocaleEN), MaxSeries, MaxCategories)
	if len(lp.labels) != 60 {
		t.Errorf("line kept %d of 60 points; a time series must keep every one", len(lp.labels))
	}
	if len(lp.notes) != 0 {
		t.Errorf("line reported %v; nothing was capped", lp.notes)
	}
}

func TestValidateRejectsMismatchedSeries(t *testing.T) {
	cases := map[string]*spec.Chart{
		"unknown type": {Type: "spider", Labels: []string{"a"},
			Series: []spec.Series{{Values: []float64{1}}}},
		"no series": {Type: spec.ChartLine, Labels: []string{"a"}},
		"values against labels": {Type: spec.ChartBar, Labels: []string{"a", "b", "c"},
			Series: []spec.Series{{Name: "n", Values: []float64{1, 2}}}},
		"two-series pie": {Type: spec.ChartPie, Labels: []string{"a"},
			Series: []spec.Series{{Values: []float64{1}}, {Values: []float64{2}}}},
		"inverted axis": {Type: spec.ChartLine, Labels: []string{"a"},
			Series: []spec.Series{{Values: []float64{1}}},
			YAxis:  &spec.AxisSpec{Min: ptr(10.0), Max: ptr(1.0)}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := c.Validate(); err == nil {
				t.Fatal("validate accepted it")
			}
			if _, err := Render(c, idOpts()); err == nil {
				t.Fatal("render accepted it")
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestContactSheet is T-R3's gate: one sheet with every type on it in the brand
// palette, and the same sheet in greyscale, which is how an enterprise report
// is printed more often than anyone admits.
//
// It runs as a test rather than as a command so the sheet is regenerated by the
// same `go test` everything else is verified by — a gate artifact produced by a
// tool nobody runs is a gate that stops being checked. Set CHART_SHEET_DIR to
// write it; unset, it still composes both sheets in memory and asserts on them,
// so the path is exercised on every CI run.
func TestContactSheet(t *testing.T) {
	sheet := composeSheet(t)
	grey := toGreyscale(sheet)

	if ink := inkFraction(sheet); ink < 0.05 {
		t.Fatalf("contact sheet is %0.4f ink", ink)
	}
	// Greyscale is the check that matters here: if two series print as the same
	// grey the sheet is where it shows, and the palette gate in
	// packages/design-tokens is what keeps it from happening.
	if ink := inkFraction(grey); ink < 0.05 {
		t.Fatalf("greyscale sheet is %0.4f ink", ink)
	}

	dir := os.Getenv("CHART_SHEET_DIR")
	if dir == "" {
		return
	}
	writePNG(t, filepath.Join(dir, "chart-contact-sheet.png"), sheet)
	writePNG(t, filepath.Join(dir, "chart-contact-sheet-greyscale.png"), grey)
	t.Logf("wrote contact sheets to %s", dir)
}

func composeSheet(t *testing.T) *image.NRGBA {
	t.Helper()
	const (
		cols     = 2
		labelPx  = 44
		gutterPx = 24
	)
	charts := fixtures()

	cells := make([]image.Image, len(charts))
	names := make([]string, len(charts))
	for i, c := range charts {
		opts := idOpts()
		opts.WidthMM, opts.HeightMM = 110, 62
		if c.Type == spec.ChartSparkline {
			opts.HeightMM = 20
		}
		res, err := Render(c, opts)
		if err != nil {
			t.Fatalf("render %s: %v", c.Type, err)
		}
		cells[i] = decode(t, res.PNG)
		names[i] = c.Type
	}

	cellW := cells[0].Bounds().Dx()
	cellH := 0
	for _, c := range cells {
		cellH = max(cellH, c.Bounds().Dy())
	}
	rows := (len(cells) + cols - 1) / cols
	w := cols*cellW + (cols+1)*gutterPx
	h := rows*(cellH+labelPx) + (rows+1)*gutterPx

	sheet := image.NewNRGBA(image.Rect(0, 0, w, h))
	ximg.Draw(sheet, sheet.Bounds(), image.NewUniform(nrgba(theme.ColorBackground)), image.Point{}, ximg.Src)

	face := sheetFace(t)
	for i, cell := range cells {
		cx := gutterPx + (i%cols)*(cellW+gutterPx)
		cy := gutterPx + (i/cols)*(cellH+labelPx+gutterPx)

		drawText(sheet, face, names[i], cx, cy+labelPx-14)
		at := image.Rect(cx, cy+labelPx, cx+cell.Bounds().Dx(), cy+labelPx+cell.Bounds().Dy())
		ximg.Draw(sheet, at, cell, cell.Bounds().Min, ximg.Src)
	}
	return sheet
}

func sheetFace(t *testing.T) xfont.Face {
	t.Helper()
	data, err := theme.FontBytes(theme.FontMedium)
	if err != nil {
		t.Fatalf("font: %v", err)
	}
	f, err := truetype.Parse(data)
	if err != nil {
		t.Fatalf("parse font: %v", err)
	}
	return truetype.NewFace(f, &truetype.Options{Size: 24, DPI: 72})
}

func drawText(dst ximg.Image, face xfont.Face, s string, x, y int) {
	d := &xfont.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(nrgba(theme.ColorForeground)),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

// toGreyscale maps each pixel to the grey a monochrome printer produces: linear
// luminance, re-encoded to sRGB. Same transform as `greyscale()` in
// packages/design-tokens/lib/color.mjs, which is what the palette gate uses —
// the sheet and the gate have to be looking at the same thing.
func toGreyscale(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			l := 0.2126*srgbToLinear(c.R) + 0.7152*srgbToLinear(c.G) + 0.0722*srgbToLinear(c.B)
			v := linearToSRGB(l)
			dst.Set(x, y, color.NRGBA{R: v, G: v, B: v, A: c.A})
		}
	}
	return dst
}

func srgbToLinear(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSRGB(c float64) uint8 {
	var v float64
	if c <= 0.0031308 {
		v = c * 12.92
	} else {
		v = 1.055*math.Pow(c, 1/2.4) - 0.055
	}
	return uint8(math.Round(math.Min(1, math.Max(0, v)) * 255))
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
