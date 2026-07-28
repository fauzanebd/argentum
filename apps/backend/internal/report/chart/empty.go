package chart

import (
	"github.com/go-analyze/charts"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// drawNoData draws the state a chart is in when the query came back with
// nothing.
//
// This is the whole reason the empty case is not simply "return no image". A
// blank rectangle where a chart should be reads as a rendering failure — the
// reader's first thought is that the file is broken, and their second is to
// wonder what else is missing. A framed panel that says "No data for this
// period" reads as an answer, and it is the same answer the agent would have
// given in the chat.
//
// It is drawn rather than composed out of the library's own no-data rendering
// because that one is styled by the library: its own grey, its own Roboto, its
// own size. This one is the document's border, the document's muted grey and
// the document's type at the caption size, which is what makes it look like a
// deliberate part of the report.
func drawNoData(p *charts.Painter, g geometry, lab labels) {
	w, h := p.Width(), p.Height()
	inset := int(g.mmPx(0.5))

	p.FilledRect(
		inset, inset, w-inset, h-inset,
		rgba(theme.ColorSurfaceMuted),
		rgba(theme.ColorBorder),
		g.mmPx(theme.Page.Hairline),
	)

	font := charts.FontStyle{
		FontSize:  g.ptPx(theme.TypeScale.Body),
		FontColor: rgba(theme.ColorMuted),
	}
	box := p.MeasureText(lab.noData, 0, font)
	// MeasureText returns a box whose height is the ascent-to-descent span and
	// whose origin is the baseline, so the y here is the centre line pushed
	// down by half the text height — not the top of it.
	p.Text(lab.noData, (w-box.Width())/2, (h+box.Height())/2, 0, font)
}
