// Package theme is the report renderer's half of the shared design system.
//
// Every value in tokens_gen.go comes from packages/design-tokens/tokens.json,
// the same file the dashboard's CSS variables are generated from, so a colour
// or a type size cannot drift between the screen and the document without CI
// noticing (see `make tokens`).
//
// This file holds what a generator should not decide: the types the values are
// poured into, and the geometry derived from them. Nothing here is generated,
// and nothing here is a token — if you find yourself hard-coding a colour or a
// millimetre below, it belongs in tokens.json instead.
package theme

import "github.com/johnfercher/maroto/v2/pkg/props"

// Color is an 8-bit-per-channel RGB colour. Documents are print artifacts, so
// there is no alpha: maroto composites nothing, and a translucent fill on paper
// is whatever the printer decides it is.
type Color struct {
	R, G, B uint8
}

// Props converts to maroto's colour type. maroto takes *props.Color everywhere
// and treats nil as "inherit", which is why this returns a pointer.
func (c Color) Props() *props.Color {
	return &props.Color{Red: int(c.R), Green: int(c.G), Blue: int(c.B)}
}

// Hex renders the colour the way tokens.json writes it, for log lines and for
// the PPTX renderer (T-R4), where OOXML wants `RRGGBB` without the hash.
func (c Color) Hex() string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		'#',
		digits[c.R>>4], digits[c.R&0x0F],
		digits[c.G>>4], digits[c.G&0x0F],
		digits[c.B>>4], digits[c.B&0x0F],
	})
}

// Mix blends towards other by t (0 keeps c, 1 returns other).
//
// Derived colour rather than a token: a callout's tint is a function of its
// tone, and adding "primary at 8%" to tokens.json would put a rendering
// decision in the file the dashboard also reads.
func (c Color) Mix(other Color, t float64) Color {
	if t <= 0 {
		return c
	}
	if t >= 1 {
		return other
	}
	blend := func(a, b uint8) uint8 {
		return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5)
	}
	return Color{
		R: blend(c.R, other.R),
		G: blend(c.G, other.G),
		B: blend(c.B, other.B),
	}
}

// Tint is Mix towards white: the fill under a callout or a KPI card, light
// enough that body text on top of it still clears contrast on a laser printer.
func (c Color) Tint(t float64) Color {
	return c.Mix(Color{R: 0xFF, G: 0xFF, B: 0xFF}, t)
}

// TypeScaleTokens is the print type scale, in points.
type TypeScaleTokens struct {
	Display float64
	H1      float64
	H2      float64
	Body    float64
	Caption float64
}

// SpacingTokens is the vertical rhythm, in millimetres.
type SpacingTokens struct {
	XS float64
	SM float64
	MD float64
	LG float64
	XL float64
}

// PageTokens is page geometry, in millimetres.
type PageTokens struct {
	Width             float64
	Height            float64
	Margin            float64
	HeaderHeight      float64
	FooterHeight      float64
	TableHeaderHeight float64
	TableRowHeight    float64
	Hairline          float64
}

// ContentWidth is the usable width between the left and right margins. maroto
// works in a 12-column grid over exactly this width, so column arithmetic in
// T-R2 measures against this rather than against the page.
func (p PageTokens) ContentWidth() float64 { return p.Width - 2*p.Margin }

// ContentHeight is the usable height once the running header and footer are
// taken out. This is the number a table pager needs: rows that fit on a page.
func (p PageTokens) ContentHeight() float64 {
	return p.Height - 2*p.Margin - p.HeaderHeight - p.FooterHeight
}

// GridCols is the number of columns maroto divides the content width into.
//
// maroto defaults to 12, which is a layout grid rather than a measuring stick:
// an 8-column table can only be 1-1-1-1-2-2-2-2 units wide, so the one column
// holding a product name gets the same 26mm as the one holding "Qty". 120 is
// the same grid at ten times the resolution — a column can be 7.5% of the
// width or 12.5%, and the content-weighted widths T-R2 computes survive the
// rounding to integers.
//
// It is not a token: nothing on the dashboard has a 120-column grid, and a
// value that means something in exactly one renderer belongs beside that
// renderer's other geometry.
const GridCols = 120

// SeriesColor returns the palette entry for a zero-based chart series index,
// wrapping when a chart has more series than the palette has rungs. Wrapping is
// a last resort and not a licence to draw 20 series: T-R3 caps series at 8 and
// buckets the rest, which is what keeps the palette's L* separation meaningful.
func SeriesColor(i int) Color {
	if i < 0 {
		i = 0
	}
	return ChartPalette[i%len(ChartPalette)]
}
