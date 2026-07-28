package pptx

import (
	"math"

	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Slide geometry and the deck's type scale.
//
// Everything here is derived from the design tokens or from the OOXML slide
// size, and nothing here is a token: a 16:9 slide has no counterpart on the
// dashboard, and a value that means something in exactly one renderer belongs
// beside that renderer's other geometry — the same argument theme.GridCols
// makes for the PDF.

// EMU is the English Metric Unit, OOXML's length. 914400 to the inch, which
// makes exactly 36000 to the millimetre — chosen by the format precisely so
// both inches and centimetres divide it without remainder.
const emuPerMM = 36000.0

// The 16:9 slide, at OOXML's standard widescreen size: 13⅓ × 7½ inches, which
// is 338.67 × 190.5 mm. 4:3 is not offered. A deck that opens letterboxed on
// every projector built since 2010 is not a compatibility win.
const (
	slideWidthEMU  = 12192000
	slideHeightEMU = 6858000

	// The notes page is portrait Letter, which is what PowerPoint writes and
	// what every reader expects when it prints a notes handout.
	notesWidthEMU  = 6858000
	notesHeightEMU = 9144000
)

var (
	slideWidthMM  = float64(slideWidthEMU) / emuPerMM  // 338.667
	slideHeightMM = float64(slideHeightEMU) / emuPerMM // 190.5
)

// Margins, in millimetres. Wider than the page's because the slide is wider:
// the measure a reader's eye tracks is the same either way, and a line of text
// running the full 339mm of a slide is unreadable at any size.
const (
	marginX      = 24.0
	marginTop    = 17.0
	marginBottom = 12.0

	// footerBand is the strip at the foot of every content slide carrying the
	// confidentiality label and the slide number.
	footerBand = 7.0

	// titleBand is the height reserved for a slide title. Two lines of the
	// deck's H1 plus its leading; a third line is truncated rather than allowed
	// to push the content down, because a slide title that long is the
	// problem, not the layout.
	titleBand = 22.0

	// titleRule is the short brand-coloured rule under a slide title — the same
	// device the PDF's level-1 headings use, at slide scale.
	titleRuleWidth     = 34.0
	titleRuleThickness = 1.6
)

// deckScale lifts the print type scale onto a slide.
//
// It is one number rather than a second scale in tokens.json, because the deck
// is the same design system seen from further away — decoupling the two would
// let a body-size change land in the report and not in the deck built from the
// same spec.
//
// 1.8 comes from the measure: the slide's content width is 290.7mm against
// A4's 174mm, a ratio of 1.67, rounded up because a slide is read across a
// room and a page is read at arm's length.
const deckScale = 1.8

// deckType is the print type scale at slide scale, in points.
var deckType = theme.TypeScaleTokens{
	Display: scalePt(theme.TypeScale.Display), // 43.5 — cover and divider titles
	H1:      scalePt(theme.TypeScale.H1),      // 29   — slide titles
	H2:      scalePt(theme.TypeScale.H2),      // 23.5 — leads, KPI values, table captions
	Body:    scalePt(theme.TypeScale.Body),    // 18   — bullets and table cells
	Caption: scalePt(theme.TypeScale.Caption), // 14.5 — footers, labels, chart captions
}

// scalePt rounds to the nearest half point. OOXML carries hundredths of a
// point, so any value would encode; halves keep the scale legible in the XML
// and keep two sizes from differing by a tenth of a point nobody can see.
func scalePt(pt float64) float64 {
	return math.Round(pt*deckScale*2) / 2
}

// contentWidth is the usable width between the left and right margins.
func contentWidth() float64 { return slideWidthMM - 2*marginX }

// bodyTop is the top of a content slide's body area: under the title band and
// the rule beneath it.
func bodyTop() float64 { return marginTop + titleBand + theme.Spacing.MD }

// bodyHeight is what is left for content once the title and footer are taken.
func bodyHeight() float64 { return slideHeightMM - bodyTop() - marginBottom - footerBand }

// footerTop is the baseline strip of a content slide.
func footerTop() float64 { return slideHeightMM - marginBottom - footerBand }

// bodyLeading is the multiple of the font height a line of slide copy occupies.
// Looser than the PDF's 1.32: a slide is read at distance and from an angle,
// and tight leading is the first thing that fails in both conditions.
const bodyLeading = 1.45

// substitutionMargin is the width every text estimate is measured against,
// as a fraction of the box it has to fit in.
//
// The deck names Space Grotesk and does not embed it (see the package comment),
// so the machine that opens the file may set the text in whatever its own
// substitution picks — Arial on Windows, Helvetica Neue on macOS, Liberation
// Sans on Linux. Those are within a few percent of Space Grotesk's widths but
// they are not identical, and the direction that matters is wider. Measuring
// against 94% of the real box is what keeps a line that just fits here from
// being a line that just does not fit there.
const substitutionMargin = 0.94

// linesIn is how many lines s will take in a box of the given width, measured
// against the embedded face and discounted for a substituted one.
func linesIn(s string, family string, style measure.Style, sizePt, widthMM float64) int {
	if s == "" {
		return 0
	}
	return len(measure.Wrap(s, family, style, sizePt, widthMM*substitutionMargin))
}

// textHeight is the height s will occupy in a box of the given width.
func textHeight(s string, family string, style measure.Style, sizePt, widthMM float64) float64 {
	return float64(linesIn(s, family, style, sizePt, widthMM)) * measure.LineHeightMM(sizePt) * bodyLeading
}

// fitLines truncates s to at most maxLines in a box of the given width, with a
// visible ellipsis when it had to cut. Silent clipping is what PowerPoint does
// on its own and what this ticket calls an acceptance failure.
func fitLines(s string, family string, style measure.Style, sizePt, widthMM float64, maxLines int) string {
	return measure.Fit(s, family, style, sizePt, widthMM*substitutionMargin, maxLines)
}

// mmToEMU converts a millimetre measurement to OOXML's unit, rounded to the
// nearest whole EMU — the format takes integers.
func mmToEMU(mm float64) int64 { return int64(math.Round(mm * emuPerMM)) }

// ptToHundredths converts a point size to OOXML's `sz` attribute, which counts
// hundredths of a point.
func ptToHundredths(pt float64) int { return int(math.Round(pt * 100)) }
