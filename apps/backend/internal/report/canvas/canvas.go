// Package canvas is the 16:9 surface and the type scale that goes on it.
//
// It was the deck renderer's geometry.go until the video renderer needed the
// same numbers (T-V1). The extraction is the same move T-R4 made with measure,
// layout and labels, for the same reason: two renderers that decide
// independently how wide the measure is and how big a slide title is will
// eventually decide differently, and the difference shows up as the same report
// proportioned two ways.
//
// **The video frame and the PowerPoint slide are the same surface.** OOXML's
// widescreen slide is 13⅓ × 7½ inches — 338.667 × 190.5 mm — and 1920 × 1080 px
// maps onto it at exactly 5.669 px/mm, which is exactly **2 px per point**. That
// is not a coincidence worth hiding in a constant: it means a line that fits on
// a slide fits in a frame, a 29pt slide title is a 58px frame title, and the two
// renderers cannot disagree about wrapping without one of them ignoring this
// package.
//
// Everything here is derived from the design tokens or from the slide size, and
// nothing here is a token: a 16:9 surface has no counterpart on the dashboard,
// and a value that means something in exactly two renderers belongs beside their
// other geometry — the same argument theme.GridCols makes for the PDF.
package canvas

import (
	"math"

	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The 16:9 surface, in millimetres. 4:3 is not offered: a deck that opens
// letterboxed on every projector built since 2010 is not a compatibility win,
// and a 4:3 video is not a thing anyone asks for at all.
//
// **Written as the division rather than as 338.667**, which is what it was for
// one commit. OOXML's widescreen slide is 12192000 × 6858000 EMU and the deck
// renderer derives its millimetres from those; a three-decimal literal here is
// 0.00033mm wider, which sounds like nothing and is not. That difference reaches
// every measured column width, every wrap decision and every EMU rounding in the
// package, and it changed all five deck fixtures — between 29 and 375 bytes each
// — while every test still passed. The lesson is the one this tree keeps
// learning: a number copied into a second place stops tracking the first.
const (
	WidthMM  = 12192000.0 / 36000.0 // 338.6667
	HeightMM = 6858000.0 / 36000.0  // 190.5
)

// PxPerMM maps the surface onto a 1920×1080 frame. See the package comment for
// why this works out to exactly 2 px per point.
const PxPerMM = 1920.0 / WidthMM

// PxPerPt is the same factor expressed the way a caller usually wants it: a
// point size in, a CSS pixel size out.
const PxPerPt = PxPerMM * measure.MMPerPoint

// Margins, in millimetres. Wider than the page's because the surface is wider:
// the measure a reader's eye tracks is the same either way, and a line of text
// running the full 339mm is unreadable at any size.
const (
	MarginX      = 24.0
	MarginTop    = 17.0
	MarginBottom = 12.0

	// FooterBand is the strip at the foot of every content slide carrying the
	// confidentiality label and the slide number. The video has no slide
	// numbers but keeps the band, because the alternative is content sitting
	// 7mm lower in one renderer than the other.
	FooterBand = 7.0

	// TitleBand is the height reserved for a title. Two lines of H1 plus its
	// leading; a third line is truncated rather than allowed to push the
	// content down, because a title that long is the problem, not the layout.
	TitleBand = 22.0

	// TitleRuleWidth and TitleRuleThickness are the short brand-coloured rule
	// under a title — the same device the PDF's level-1 headings use, at
	// surface scale.
	TitleRuleWidth     = 34.0
	TitleRuleThickness = 1.6
)

// Scale lifts the print type scale onto the surface.
//
// It is one number rather than a second scale in tokens.json, because this is
// the same design system seen from further away — decoupling the two would let
// a body-size change land in the report and not in the deck or the video built
// from the same spec.
//
// 1.8 comes from the measure: the content width is 290.7mm against A4's 174mm,
// a ratio of 1.67, rounded up because a slide is read across a room and a page
// is read at arm's length.
const Scale = 1.8

// Type is the print type scale at surface scale, in points.
var Type = theme.TypeScaleTokens{
	Display: ScalePt(theme.TypeScale.Display), // 43.5 — cover and divider titles
	H1:      ScalePt(theme.TypeScale.H1),      // 29   — slide titles
	H2:      ScalePt(theme.TypeScale.H2),      // 23.5 — leads, KPI values, table captions
	Body:    ScalePt(theme.TypeScale.Body),    // 18   — bullets and table cells
	Caption: ScalePt(theme.TypeScale.Caption), // 14.5 — footers, labels, chart captions
}

// ScalePt rounds to the nearest half point. OOXML carries hundredths of a
// point, so any value would encode; halves keep the scale legible in the XML,
// keep two sizes from differing by a tenth of a point nobody can see, and — at
// 2 px per point — keep every video type size on a whole pixel.
func ScalePt(pt float64) float64 {
	return math.Round(pt*Scale*2) / 2
}

// ContentWidth is the usable width between the left and right margins.
func ContentWidth() float64 { return WidthMM - 2*MarginX }

// BodyTop is the top of a content surface's body area: under the title band and
// the rule beneath it.
func BodyTop() float64 { return MarginTop + TitleBand + theme.Spacing.MD }

// BodyHeight is what is left for content once the title and footer are taken.
func BodyHeight() float64 { return HeightMM - BodyTop() - MarginBottom - FooterBand }

// FooterTop is the baseline strip of a content surface.
func FooterTop() float64 { return HeightMM - MarginBottom - FooterBand }

// BodyLeading is the multiple of the font height a line of copy occupies.
// Looser than the PDF's 1.32: this surface is read at distance and from an
// angle, and tight leading is the first thing that fails in both conditions.
const BodyLeading = 1.45

// SubstitutionMargin is the width every text estimate is measured against, as a
// fraction of the box it has to fit in.
//
// The deck names Space Grotesk and does not embed it, so the machine that opens
// the file may set the text in whatever its own substitution picks — Arial on
// Windows, Helvetica Neue on macOS, Liberation Sans on Linux. Those are within a
// few percent of Space Grotesk's widths but they are not identical, and the
// direction that matters is wider. Measuring against 94% of the real box is what
// keeps a line that just fits here from being a line that just does not fit
// there.
//
// **The video does not have that problem and pays the margin anyway.** Its
// renderer loads the vendored face before the first frame (T-V2), so nothing is
// ever substituted. Measuring it identically is worth more than the 6%: it means
// a fixture that fits on a slide fits in a frame, so the two renderers can share
// a wrapping test rather than each having their own idea of tight.
const SubstitutionMargin = 0.94

// LinesIn is how many lines s will take in a box of the given width, measured
// against the embedded face and discounted for a substituted one.
func LinesIn(s string, family string, style measure.Style, sizePt, widthMM float64) int {
	if s == "" {
		return 0
	}
	return len(measure.Wrap(s, family, style, sizePt, widthMM*SubstitutionMargin))
}

// Wrap breaks s into the lines it will actually occupy in a box of the given
// width. The video renderer emits these as separate lines rather than letting a
// browser re-wrap, which is what makes "Go decides everything" true of line
// breaks and not only of numbers.
func Wrap(s string, family string, style measure.Style, sizePt, widthMM float64) []string {
	if s == "" {
		return nil
	}
	return measure.Wrap(s, family, style, sizePt, widthMM*SubstitutionMargin)
}

// TextHeight is the height s will occupy in a box of the given width.
func TextHeight(s string, family string, style measure.Style, sizePt, widthMM float64) float64 {
	return float64(LinesIn(s, family, style, sizePt, widthMM)) * measure.LineHeightMM(sizePt) * BodyLeading
}

// FitLines truncates s to at most maxLines in a box of the given width, with a
// visible ellipsis when it had to cut. Silent clipping is what PowerPoint does
// on its own and what T-R4 calls an acceptance failure; a frame that clips is
// worse still, because the viewer cannot scroll.
func FitLines(s string, family string, style measure.Style, sizePt, widthMM float64, maxLines int) string {
	return measure.Fit(s, family, style, sizePt, widthMM*SubstitutionMargin, maxLines)
}

// MaxFactLines is how far a fact's value may wrap. Three lines holds a postal
// address, which is what a key_value block is for.
const MaxFactLines = 3

// FactLabelShare is the width of a fact's label column, as a fraction of the
// measure.
const FactLabelShare = 0.34

// FactRowHeight is the height one label/value row occupies.
//
// The value may wrap and it has to: the first thing a key_value block carries
// in practice is a billing address, and "Meridian Logistics Pte Ltd, 8 Marina
// View #22-01, Asia…" truncated to one line is an invoice missing the address
// it is addressed to. So a block is packed by measured height rather than by a
// fixed rows-per-surface, and both renderers pack it the same way — a fact
// block that splits after row seven in the deck and after row nine in the video
// is the same document disagreeing with itself.
func FactRowHeight(value string) float64 {
	valueW := ContentWidth() * (1 - FactLabelShare)
	lines := max(1, min(LinesIn(value, theme.FontBody, measure.Bold, Type.Body, valueW), MaxFactLines))
	return float64(lines)*measure.LineHeightMM(Type.Body)*BodyLeading + theme.Spacing.SM
}

// ChartAspect is the height a chart takes as a fraction of its width when the
// body area allows it. 0.39 is close to the golden ratio's reciprocal and is
// what the PDF's 68mm-on-174mm default works out to, so the same chart is the
// same shape in all three renderers — a bar that looks steep in the deck and
// shallow in the report is the same figure telling two stories.
const ChartAspect = 0.39

// MaxChartHeight is the tallest a chart may be drawn: the body area less the
// caption line under it.
func MaxChartHeight() float64 {
	return BodyHeight() - measure.LineHeightMM(Type.Caption)*BodyLeading - theme.Spacing.MD
}

// Px converts a millimetre measurement on this surface to whole CSS pixels.
func Px(mm float64) int { return int(math.Round(mm * PxPerMM)) }

// PtPx converts a point size to whole CSS pixels.
func PtPx(pt float64) int { return int(math.Round(pt * PxPerPt)) }
