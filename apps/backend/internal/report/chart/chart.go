// Package chart renders a spec chart section into a PNG.
//
// One image, both renderers. The PDF embeds it and the deck (T-R4) embeds the
// same bytes, because a chart that is drawn twice is a chart that can disagree
// with itself — a different axis maximum in the deck than in the report it was
// attached to is the kind of discrepancy a reader notices and cannot explain.
//
// Everything visible comes from the design tokens: the series colours are
// ChartPalette, the axis rules are the table rule's grey, the labels are the
// caption's grey, and the type is Space Grotesk at the theme's caption size. A
// chart drawn in a library's default blue-and-Roboto is the single most obvious
// tell that a document was assembled rather than designed.
//
// Two decisions that are not obvious from the code:
//
//   - Output is a raster, not an SVG. maroto embeds images and PowerPoint reads
//     OOXML drawings; neither takes an SVG without a converter, and the
//     converter is a browser. So the sharpness has to come from resolution, and
//     it comes from renderDPI below.
//   - The library is github.com/go-analyze/charts, chosen over
//     wcharczuk/go-chart/v2 because it draws every one of the seven types this
//     ticket asks for — stacked bars, grouped bars and a doughnut included —
//     against one option struct, and takes a caller-supplied ColorPalette,
//     font and ValueFormatter. go-chart would have needed grouped and stacked
//     bars written by hand, which is where a chart library stops being a
//     dependency and starts being a fork.
package chart

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	"github.com/go-analyze/charts"
	xdraw "golang.org/x/image/draw"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Rendering resolution.
//
// renderDPI is what the PNG is finally written at: 200 dots per inch over the
// chart's printed size. That is well past the ~96 a screen bitmap would give
// and short of the 300 a photograph wants — a chart is flat fills and strokes,
// and the difference between 200 and 300 on that content is invisible on paper
// and doubles the file.
//
// supersample is how it is drawn. Every dimension, type size and stroke width
// is multiplied by it, the chart is rasterised at that size, and the result is
// resampled down. The library antialiases, but not across the type at these
// sizes; drawing large and shrinking is what makes a 3-pixel axis label crisp
// instead of furry. Three is the smallest factor where that is true.
const (
	renderDPI   = 200
	supersample = 3
)

// Default and bounds for the drawn size, in millimetres. The height is bounded
// rather than free because Options comes from a model-authored spec: a chart
// asking for 400mm on a 297mm page is a chart nobody sees, and the memory to
// rasterise it at 3× is real.
const (
	defaultHeightMM   = 68
	sparklineHeightMM = 16
	minHeightMM       = 20
	maxHeightMM       = 130
	maxWidthMM        = 200
)

// Options is everything the renderer needs that the spec does not carry.
type Options struct {
	// WidthMM is the drawn width. Zero takes the A4 content width, which is
	// what a full-measure chart in a report is.
	WidthMM float64

	// HeightMM overrides the spec's own height. Zero defers to the spec, then
	// to the type's default.
	HeightMM float64

	// Format is the document's locale and currency. Axis ticks, value labels
	// and the legend all format through it, so the number under a bar and the
	// number in the table beneath it are written the same way.
	Format format.Options

	// MaxSeries and MaxCategories override the caps. Zero takes the defaults.
	MaxSeries     int
	MaxCategories int
}

// Result is a rendered chart plus what the caller has to say about it.
type Result struct {
	// PNG is the image, at WidthPx × HeightPx.
	PNG      []byte
	WidthPx  int
	HeightPx int
	WidthMM  float64
	HeightMM float64

	// Note is a sentence for the caption when a cap fired, or empty. It is
	// returned rather than drawn into the image because a caption belongs in
	// the document's type, not in a bitmap: it has to wrap, it has to be
	// selectable, and it has to be the same grey as every other caption.
	Note string

	// Empty is true when there was nothing to plot. The image is still valid
	// and still says so — see empty.go. The flag is for a caller that wants to
	// suppress the chart's title as well, and for tests.
	Empty bool
}

// Render draws a chart.
//
// It is deterministic: the same spec and the same options produce the same
// bytes, which is what lets a document render twice into one file (T-R2) and
// what lets a test assert on a chart at all.
func Render(c *spec.Chart, opts Options) (Result, error) {
	if c == nil {
		return Result{}, fmt.Errorf("chart: nil chart")
	}
	if err := c.Validate(); err != nil {
		return Result{}, err
	}

	lab := labelsFor(opts.Format.Locale)
	maxSeries, maxCategories := opts.MaxSeries, opts.MaxCategories
	if maxSeries <= 0 {
		maxSeries = MaxSeries
	}
	if maxCategories <= 0 {
		maxCategories = MaxCategories
	}
	p := normalize(c, lab, maxSeries, maxCategories)

	g := geometryFor(c, opts)
	font, err := chartFont()
	if err != nil {
		return Result{}, err
	}

	painter := charts.NewPainter(charts.PainterOptions{
		OutputFormat: charts.ChartOutputPNG,
		Width:        g.drawWidthPx,
		Height:       g.drawHeightPx,
		Font:         font,
		Theme:        palette(),
	})

	if p.empty {
		drawNoData(painter, g, lab)
	} else if err := draw(painter, c, p, g, opts); err != nil {
		return Result{}, err
	}

	raw, err := painter.Bytes()
	if err != nil {
		return Result{}, fmt.Errorf("chart: rasterise: %w", err)
	}
	out, err := downscale(raw, g.widthPx, g.heightPx)
	if err != nil {
		return Result{}, err
	}

	return Result{
		PNG:      out,
		WidthPx:  g.widthPx,
		HeightPx: g.heightPx,
		WidthMM:  g.widthMM,
		HeightMM: g.heightMM,
		Note:     joinNotes(p.notes),
		Empty:    p.empty,
	}, nil
}

// HeightMM is the height Render will draw this chart at, without drawing it.
//
// It exists for the page-fit checks in the PDF renderer, which have to know
// whether a chart fits beside its heading before deciding to emit either.
// Rasterising a chart to find out how tall it is would cost a full render per
// heading, and the answer does not depend on the pixels.
func HeightMM(c *spec.Chart, opts Options) float64 {
	if c == nil {
		return 0
	}
	return geometryFor(c, opts).heightMM
}

// geometry is the one place millimetres, points and pixels meet. Everything
// downstream works in device pixels at the supersampled scale.
type geometry struct {
	widthMM, heightMM         float64
	widthPx, heightPx         int
	drawWidthPx, drawHeightPx int
}

func geometryFor(c *spec.Chart, opts Options) geometry {
	widthMM := opts.WidthMM
	if widthMM <= 0 {
		widthMM = theme.Page.ContentWidth()
	}
	widthMM = math.Min(widthMM, maxWidthMM)

	heightMM := opts.HeightMM
	if heightMM <= 0 {
		heightMM = c.HeightMM
	}
	if heightMM <= 0 {
		heightMM = defaultHeightMM
		if c.Type == spec.ChartSparkline {
			heightMM = sparklineHeightMM
		}
	}
	// A sparkline is allowed below the floor: it is a KPI-card ornament, and
	// 20mm of it inside a 23mm card is not a sparkline, it is a chart.
	floor := float64(minHeightMM)
	if c.Type == spec.ChartSparkline {
		floor = 6
	}
	heightMM = math.Max(floor, math.Min(heightMM, maxHeightMM))

	g := geometry{
		widthMM:  widthMM,
		heightMM: heightMM,
		widthPx:  pxFor(widthMM),
		heightPx: pxFor(heightMM),
	}
	g.drawWidthPx = g.widthPx * supersample
	g.drawHeightPx = g.heightPx * supersample
	return g
}

// pxFor converts millimetres to output pixels at renderDPI.
func pxFor(mm float64) int {
	return int(math.Round(mm / 25.4 * renderDPI))
}

// libraryFontDPI is the DPI the charts library rasterises type at, and it is
// not 72. chartdraw's defaultDPI is 92, so a FontStyle asking for size N puts
// N × 92/72 pixels of glyph on the canvas. Dividing it back out is what makes
// theme.TypeScale.Caption mean 8 typographic points on the printed page rather
// than 8 of something.
//
// It is read from the library's constant by arithmetic rather than by import
// because chartdraw does not export it. If a future version changes it, the
// symptom is chart type that is uniformly the wrong size against the caption
// beneath it, which is visible on the contact sheet.
const libraryFontDPI = 92.0

// ptPx converts a point size from the theme's type scale to the size the
// library wants, at the supersampled scale. A caption that is 8pt in the
// document has to arrive here as however many of the library's units 8pt is at
// the resolution being drawn at — not as 8.
func (g geometry) ptPx(pt float64) float64 {
	return pt / 72 * renderDPI * supersample * (72 / libraryFontDPI)
}

// mmPx converts a millimetre measurement — a stroke width, a padding — to
// supersampled device pixels.
func (g geometry) mmPx(mm float64) float64 {
	return mm / 25.4 * renderDPI * supersample
}

// downscale resamples the supersampled raster to its final size.
//
// CatmullRom rather than a box filter: it is the sharpest of the resamplers in
// x/image that does not ring, and the content here is high-contrast strokes on
// a flat ground, which is exactly where a box filter turns a hairline into a
// smudge. The cost is a few milliseconds on an image this size.
func downscale(raw []byte, widthPx, heightPx int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("chart: decode raster: %w", err)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, widthPx, heightPx))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)

	var buf bytes.Buffer
	// The default compression level is a fixed function of the input, and
	// image/png writes no timestamp, so the same pixels always encode to the
	// same bytes. That is what Result's determinism rests on.
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("chart: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

func joinNotes(notes []string) string {
	out := ""
	for _, n := range notes {
		if n == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += n
	}
	return out
}
