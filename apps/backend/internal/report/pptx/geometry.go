package pptx

import (
	"math"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/measure"
)

// Slide geometry, and where it now comes from.
//
// Everything about the *surface* — its size, its margins, the title band, the
// type scale, the leading, the substitution margin and the text fitting — moved
// to internal/report/canvas when the video renderer needed the same numbers
// (T-V1). This file is what is left: OOXML's units, and thin local names for
// the shared values so the deck's call sites read the way they always did.
//
// The delegations are deliberate rather than lazy. Renaming ~80 call sites to
// canvas.X would have made the T-V1 diff unreviewable against a renderer whose
// only guarantee is that its bytes did not change.
//
// Since T-G2 the surface is a value, canvas.Wide, and this renderer names it
// exactly once — here. A deck is a 16:9 artifact by format (OOXML's widescreen
// slide is the surface), so the choice is not a parameter of the renderer the
// way it is of the video builder, and every call site below keeps reading the
// local name it always did.

// EMU is the English Metric Unit, OOXML's length. 914400 to the inch, which
// makes exactly 36000 to the millimetre — chosen by the format precisely so
// both inches and centimetres divide it without remainder.
const emuPerMM = 36000.0

// The 16:9 slide, at OOXML's standard widescreen size: 13⅓ × 7½ inches, which
// is 338.67 × 190.5 mm — canvas.WidthMM and canvas.HeightMM, in EMU.
const (
	slideWidthEMU  = 12192000
	slideHeightEMU = 6858000

	// The notes page is portrait Letter, which is what PowerPoint writes and
	// what every reader expects when it prints a notes handout.
	notesWidthEMU  = 6858000
	notesHeightEMU = 9144000
)

// slideHeightMM is canvas.HeightMM derived from the EMU rather than imported,
// so the two constants are checked against each other by a test rather than
// asserted to match by a comment.
var slideHeightMM = float64(slideHeightEMU) / emuPerMM // 190.5

// surface is the one surface a deck is drawn on.
var surface = canvas.Wide

// Margins and bands, from the shared surface. Variables rather than constants
// since T-G2, because a Surface field is not a constant expression; nothing
// here was ever used where a constant is required.
var (
	marginX      = surface.MarginX
	marginTop    = surface.MarginTop
	marginBottom = surface.MarginBottom

	footerBand = surface.FooterBand
	titleBand  = surface.TitleBand

	titleRuleWidth     = surface.TitleRuleWidth
	titleRuleThickness = surface.TitleRuleThickness
)

// deckType is the shared type scale under the name this renderer has always
// used for it.
var deckType = surface.Type

// contentWidth is the usable width between the left and right margins.
func contentWidth() float64 { return surface.ContentWidth() }

// bodyTop is the top of a content slide's body area: under the title band and
// the rule beneath it.
func bodyTop() float64 { return surface.BodyTop() }

// bodyHeight is what is left for content once the title and footer are taken.
func bodyHeight() float64 { return surface.BodyHeight() }

// footerTop is the baseline strip of a content slide.
func footerTop() float64 { return surface.FooterTop() }

// bodyLeading is the multiple of the font height a line of slide copy occupies.
const bodyLeading = canvas.BodyLeading

// substitutionMargin is the width every text estimate is measured against, as a
// fraction of the box it has to fit in. See canvas for why it is 94% and why
// the video renderer pays it too.
const substitutionMargin = canvas.SubstitutionMargin

// linesIn is how many lines s will take in a box of the given width, measured
// against the embedded face and discounted for a substituted one.
func linesIn(s string, family string, style measure.Style, sizePt, widthMM float64) int {
	return canvas.LinesIn(s, family, style, sizePt, widthMM)
}

// textHeight is the height s will occupy in a box of the given width.
func textHeight(s string, family string, style measure.Style, sizePt, widthMM float64) float64 {
	return canvas.TextHeight(s, family, style, sizePt, widthMM)
}

// fitLines truncates s to at most maxLines in a box of the given width, with a
// visible ellipsis when it had to cut. Silent clipping is what PowerPoint does
// on its own and what T-R4 calls an acceptance failure.
func fitLines(s string, family string, style measure.Style, sizePt, widthMM float64, maxLines int) string {
	return canvas.FitLines(s, family, style, sizePt, widthMM, maxLines)
}

// mmToEMU converts a millimetre measurement to OOXML's unit, rounded to the
// nearest whole EMU — the format takes integers.
func mmToEMU(mm float64) int64 { return int64(math.Round(mm * emuPerMM)) }

// ptToHundredths converts a point size to OOXML's `sz` attribute, which counts
// hundredths of a point.
func ptToHundredths(pt float64) int { return int(math.Round(pt * 100)) }
