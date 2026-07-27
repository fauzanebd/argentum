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
