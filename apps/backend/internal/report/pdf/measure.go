package pdf

import (
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"

	"github.com/fauzanebd/argentum/internal/report/measure"
)

// Text measurement lives in internal/report/measure, because the deck renderer
// needs the same answers and two measurers would eventually disagree. What
// stays here is the translation between maroto's vocabulary — fontstyle.Type,
// props.Text — and the shared package's, plus the one height calculation that
// is maroto-specific.

// gofpdf writes its font catalogue in Go map order, so the same spec rendered
// twice produces the same pages with the font objects numbered differently —
// different bytes, no golden test, and a diff between two builds that says
// nothing. SetDefaultCatalogSort is the library's own switch for this: it sorts
// the resource catalogues instead, and every Fpdf created afterwards inherits
// it, including the one maroto builds inside itself where there is no other way
// to reach it.
//
// It is a package-level global in gofpdf, which is why this is an init and not
// a call site. It stays in this package rather than moving with the measurer
// because it is about how a PDF is written, not about how text is measured.
func init() { gofpdf.SetDefaultCatalogSort(true) }

// textWidth is the rendered width of s in millimetres.
func textWidth(s string, family string, style fontstyle.Type, size float64) float64 {
	return measure.Width(s, family, measure.Style(style), size)
}

// lineHeight is the height of one line of text at a given point size, in mm.
func lineHeight(sizePt float64) float64 { return measure.LineHeightMM(sizePt) }

// wrapLines splits s the way maroto will, given a column width in millimetres.
func wrapLines(s string, family string, style fontstyle.Type, size, colWidth float64) []string {
	return measure.Wrap(s, family, measure.Style(style), size, colWidth)
}

// textHeight is the height a text component will occupy, matching
// text.Text.GetHeight: one font height per line, plus the vertical padding
// between them, plus the top and bottom offsets.
func textHeight(s string, family string, style fontstyle.Type, size, colWidth float64, p props.Text) float64 {
	lines := len(wrapLines(s, family, style, size, colWidth-p.Left-p.Right))
	h := float64(lines)*lineHeight(size) + float64(lines-1)*p.VerticalPadding
	return h + p.Top + p.Bottom
}

// truncateToLines shortens s until it wraps into at most maxLines, appending an
// ellipsis when it had to cut.
func truncateToLines(s string, family string, style fontstyle.Type, size, colWidth float64, maxLines int) string {
	return measure.Truncate(s, family, measure.Style(style), size, colWidth, maxLines)
}

// fitText is truncateToLines plus the case it cannot handle: a token with no
// spaces in it, already on one line and already too wide for its column.
func fitText(s string, family string, style fontstyle.Type, size, colWidth float64, maxLines int) string {
	return measure.Fit(s, family, measure.Style(style), size, colWidth, maxLines)
}
