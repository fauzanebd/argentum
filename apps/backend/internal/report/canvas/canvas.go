// Package canvas is the surface a slide or a frame is drawn on, and the type
// scale that goes on it.
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
// **A surface is a value, and this package ships one of them.** Until T-G2 the
// 16:9 geometry was a set of package constants, which was right for as long as
// there was one surface. A portrait carousel (T-G3) is the same measuring code
// against a second geometry, and the plan's own comment says why that cannot be
// a second set of constants: a plan measured for one width is not a plan for
// any other, because its line breaks were decided against that width. So the
// geometry is a Surface, Wide is the 16:9 instance, and everything that used to
// read a constant now reads a field of the surface it was handed. What does
// **not** vary by surface — the pixel density, the leading, the substitution
// margin, the table's cell padding — stays a package constant, so a reader can
// tell at the declaration which numbers a second surface may choose and which
// it may not.
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
//
// These are unexported since T-G2: they are Wide's numbers, and a caller that
// wants them reads them off Wide so that a second surface cannot be built from
// the first one's constants by accident.
const (
	wideWidthMM  = 12192000.0 / 36000.0 // 338.6667
	wideHeightMM = 6858000.0 / 36000.0  // 190.5
)

// PxPerMM maps a surface onto its frame. See the package comment for why this
// works out to exactly 2 px per point.
//
// **It is a package constant, not a Surface field, on purpose.** Every surface
// this package will ever ship is drawn at the same density — the 16:9 frame is
// 1920 px across 338.667 mm, and a portrait frame keeps the same 2 px/pt so
// that a type size measured in points lands on the same whole pixel on both.
// T-G3 chooses the portrait width *from* this constant (1080 px ÷ PxPerMM =
// 190.5 mm, which is the wide surface's height), so a surface that carried its
// own density would be a surface that could quietly stop agreeing with the
// others about what a point is.
const PxPerMM = 1920.0 / wideWidthMM

// PxPerPt is the same factor expressed the way a caller usually wants it: a
// point size in, a CSS pixel size out.
const PxPerPt = PxPerMM * measure.MMPerPoint

// Surface is one drawing surface: its size, its margins, its bands and the type
// scale set on it. Every number a renderer positions against or wraps against
// comes from one of these, and the plan a Surface measures records which one
// (Plan.Width/Height), because the line breaks in it are only right for it.
//
// Fields are exported because two renderers read them, not because anything
// outside this package should construct one: the instances are declared here,
// where the numbers are argued for.
type Surface struct {
	// WidthMM and HeightMM are the surface in millimetres; PxW and PxH are the
	// same surface in whole CSS pixels, at PxPerMM. Both are carried rather
	// than one derived from the other so a test can assert they agree.
	WidthMM, HeightMM float64
	PxW, PxH          int

	// Margins, in millimetres.
	MarginX, MarginTop, MarginBottom float64

	// FooterBand is the strip at the foot of every content surface carrying the
	// confidentiality label and the slide number. The video has no slide
	// numbers but keeps the band, because the alternative is content sitting
	// 7mm lower in one renderer than the other.
	FooterBand float64

	// TitleBand is the height reserved for a title. Two lines of H1 plus its
	// leading; a third line is truncated rather than allowed to push the
	// content down, because a title that long is the problem, not the layout.
	TitleBand float64

	// TitleRuleWidth and TitleRuleThickness are the short brand-coloured rule
	// under a title — the same device the PDF's level-1 headings use, at
	// surface scale.
	TitleRuleWidth, TitleRuleThickness float64

	// Scale lifts the print type scale onto this surface. See Wide for why it
	// is one number and not a second scale in tokens.json.
	Scale float64

	// Type is the print type scale at Scale, in points, rounded to the half
	// point by ScalePt. Filled by typeScale from Scale; the two are carried
	// together because Type is what every call site reads and Scale is what
	// the argument for it is made in.
	Type theme.TypeScaleTokens

	// MaxKPICards is how many cards a kpi_row keeps on this surface. Wide's
	// cap is the deck's — a fifth card across 291mm is narrower than the
	// number on it — and a surface that stacks its cards (T-G4) is bounded by
	// height instead, which is a different number for a different reason.
	MaxKPICards int
}

// Wide is the 16:9 surface: the PowerPoint slide and the 1920×1080 video frame.
//
// Margins are wider than the page's because the surface is wider: the measure a
// reader's eye tracks is the same either way, and a line of text running the
// full 339mm is unreadable at any size.
//
// Scale is one number rather than a second scale in tokens.json, because this
// is the same design system seen from further away — decoupling the two would
// let a body-size change land in the report and not in the deck or the video
// built from the same spec. 1.8 comes from the measure: the content width is
// 290.7mm against A4's 174mm, a ratio of 1.67, rounded up because a slide is
// read across a room and a page is read at arm's length.
var Wide = Surface{
	WidthMM:  wideWidthMM,
	HeightMM: wideHeightMM,
	PxW:      1920,
	PxH:      1080,

	MarginX:      24.0,
	MarginTop:    17.0,
	MarginBottom: 12.0,

	FooterBand: 7.0,
	TitleBand:  22.0,

	TitleRuleWidth:     34.0,
	TitleRuleThickness: 1.6,

	Scale: 1.8,
	Type:  typeScale(1.8), // Display 43, H1 29, H2 23.5, Body 18, Caption 14.5

	MaxKPICards: 4,
}

// Portrait is the 4:5 social surface: a 1080×1350 frame, which is the tallest
// ratio Instagram accepts for a carousel and the one every slide of one is
// drawn on (T-G3, decision 3).
//
// **The width is the wide surface's height, by construction.** At PxPerMM,
// 1080 px is 190.5 mm — exactly Wide.HeightMM — so a portrait frame is drawn at
// the same 2 px/pt as a landscape one and the same 18pt body is the same 36px
// body on both. Scale is Wide's for the same reason: the research asks for H1
// ≥ 56 px and body ≥ 34 px at 1080 wide, and 1.8 already gives 58 and 36, so
// a second scale would be a second type size to keep in step for nothing.
//
// The margins are the safe zones, not taste. Instagram's UI covers roughly the
// top 120 px and the bottom 150 px of a 4:5 post (username above, caption and
// actions below), so MarginTop is 22 mm (≈125 px) and MarginBottom 27 mm
// (≈153 px), and a title band or footer inside those would be drawn under the
// app's chrome. MarginX is 14 mm rather than Wide's 24: the measure is
// 162.5 mm on a surface read at arm's length, and Wide's margin exists for a
// 339 mm line read across a room.
var Portrait = Surface{
	WidthMM:  1080.0 / PxPerMM, // 190.5
	HeightMM: 1350.0 / PxPerMM, // 238.125
	PxW:      1080,
	PxH:      1350,

	MarginX:      14.0,
	MarginTop:    22.0,
	MarginBottom: 27.0,

	FooterBand: 7.0,
	TitleBand:  22.0,

	TitleRuleWidth:     34.0,
	TitleRuleThickness: 1.6,

	Scale: 1.8,
	Type:  typeScale(1.8),

	// Four stacked cards are ~120 mm of a 154 mm body; a fifth would either
	// not fit or push the title off the safe zone.
	MaxKPICards: 4,
}

// Square is the 1:1 social surface: a 1080×1080 frame (T-G11).
//
// The width, the scale and the margins are Portrait's, because they are the
// same frame seen at a different height: 1080 px wide at the same 2 px/pt, the
// same safe zones taken out of the top and the bottom, the same measure. What
// changes is what is left in between — 106.5 mm of body against Portrait's
// 154 — and the one number that has to follow it is the card cap. Four stacked
// cards need ~120 mm and there are 106.5, so a square surface keeps three and
// the builder pages the fourth exactly as it pages a long table.
var Square = Surface{
	WidthMM:  1080.0 / PxPerMM, // 190.5
	HeightMM: 1080.0 / PxPerMM, // 190.5
	PxW:      1080,
	PxH:      1080,

	MarginX:      14.0,
	MarginTop:    22.0,
	MarginBottom: 27.0,

	FooterBand: 7.0,
	TitleBand:  22.0,

	TitleRuleWidth:     34.0,
	TitleRuleThickness: 1.6,

	Scale: 1.8,
	Type:  typeScale(1.8),

	MaxKPICards: 3,
}

// Story is the 9:16 surface: a 1080×1920 frame, the full-screen format
// (T-G11).
//
// **Its margins are not Portrait's, and that is the whole reason it is a
// surface rather than a height.** A story is drawn under the app's own
// chrome: the account row and the close control take roughly the top 250 px,
// and the reply bar, the sticker row and the swipe-up affordance take roughly
// the bottom 340 px. So MarginTop is 44 mm (≈250 px) and MarginBottom 60 mm
// (≈340 px) — twice and better than twice Portrait's — and a title band or a
// footer inside either would be drawn under a control the reader cannot move.
//
// What is left is still the tallest body of any surface here, 199.7 mm against
// Portrait's 154, which is why the card cap stays at four rather than rising:
// more than four figures on one frame is a table, and that is true at any
// height.
var Story = Surface{
	WidthMM:  1080.0 / PxPerMM, // 190.5
	HeightMM: 1920.0 / PxPerMM, // 338.667
	PxW:      1080,
	PxH:      1920,

	MarginX:      14.0,
	MarginTop:    44.0,
	MarginBottom: 60.0,

	FooterBand: 7.0,
	TitleBand:  22.0,

	TitleRuleWidth:     34.0,
	TitleRuleThickness: 1.6,

	Scale: 1.8,
	Type:  typeScale(1.8),

	MaxKPICards: 4,
}

// typeScale is the print type scale at the given scale, in points.
func typeScale(scale float64) theme.TypeScaleTokens {
	return theme.TypeScaleTokens{
		Display: scalePt(theme.TypeScale.Display, scale), // cover and divider titles
		H1:      scalePt(theme.TypeScale.H1, scale),      // slide titles
		H2:      scalePt(theme.TypeScale.H2, scale),      // leads, KPI values, table captions
		Body:    scalePt(theme.TypeScale.Body, scale),    // bullets and table cells
		Caption: scalePt(theme.TypeScale.Caption, scale), // footers, labels, chart captions
	}
}

// ScalePt lifts a print point size onto this surface and rounds to the nearest
// half point. OOXML carries hundredths of a point, so any value would encode;
// halves keep the scale legible in the XML, keep two sizes from differing by a
// tenth of a point nobody can see, and — at 2 px per point — keep every video
// type size on a whole pixel.
func (s Surface) ScalePt(pt float64) float64 { return scalePt(pt, s.Scale) }

func scalePt(pt, scale float64) float64 {
	return math.Round(pt*scale*2) / 2
}

// IsZero reports whether s is the zero value, which every caller treats as
// "the wide surface" so that an Options struct that predates surfaces keeps
// meaning what it meant.
func (s Surface) IsZero() bool { return s.WidthMM == 0 }

// Or returns s, or fallback when s is the zero value.
func (s Surface) Or(fallback Surface) Surface {
	if s.IsZero() {
		return fallback
	}
	return s
}

// Portrait reports whether the surface is taller than it is wide, which is
// the question a layout asks before it stacks what it would otherwise set in
// a row. It is asked of the frame and not of the body: on the 4:5 surface the
// safe zones take enough height that the measure (162.5mm) is still wider than
// the body (154mm), so "content width < body height" — the obvious predicate —
// answers no on the one surface it exists for.
func (s Surface) Portrait() bool { return s.HeightMM > s.WidthMM }

// ContentWidth is the usable width between the left and right margins.
func (s Surface) ContentWidth() float64 { return s.WidthMM - 2*s.MarginX }

// BodyTop is the top of a content surface's body area: under the title band and
// the rule beneath it.
func (s Surface) BodyTop() float64 { return s.MarginTop + s.TitleBand + theme.Spacing.MD }

// BodyHeight is what is left for content once the title and footer are taken.
func (s Surface) BodyHeight() float64 {
	return s.HeightMM - s.BodyTop() - s.MarginBottom - s.FooterBand
}

// FooterTop is the baseline strip of a content surface.
func (s Surface) FooterTop() float64 { return s.HeightMM - s.MarginBottom - s.FooterBand }

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
//
// LinesIn, Wrap, TextHeight and FitLines take a width and read no surface:
// which box a string is measured against is the caller's decision, and the
// measurement itself is the same on every surface.
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

// FactRowHeight is the height one label/value row occupies on this surface.
//
// The value may wrap and it has to: the first thing a key_value block carries
// in practice is a billing address, and "Meridian Logistics Pte Ltd, 8 Marina
// View #22-01, Asia…" truncated to one line is an invoice missing the address
// it is addressed to. So a block is packed by measured height rather than by a
// fixed rows-per-surface, and both renderers pack it the same way — a fact
// block that splits after row seven in the deck and after row nine in the video
// is the same document disagreeing with itself.
func (s Surface) FactRowHeight(value string) float64 {
	valueW := s.ContentWidth() * (1 - FactLabelShare)
	lines := max(1, min(LinesIn(value, theme.FontBody, measure.Bold, s.Type.Body, valueW), MaxFactLines))
	return float64(lines)*measure.LineHeightMM(s.Type.Body)*BodyLeading + theme.Spacing.SM
}

// ChartAspect is the height a chart takes as a fraction of its width when the
// body area allows it. 0.39 is close to the golden ratio's reciprocal and is
// what the PDF's 68mm-on-174mm default works out to, so the same chart is the
// same shape in all three renderers — a bar that looks steep in the deck and
// shallow in the report is the same figure telling two stories.
const ChartAspect = 0.39

// MaxChartHeight is the tallest a chart may be drawn on this surface: the body
// area less the caption line under it.
func (s Surface) MaxChartHeight() float64 {
	return s.BodyHeight() - measure.LineHeightMM(s.Type.Caption)*BodyLeading - theme.Spacing.MD
}

// Px converts a millimetre measurement to whole CSS pixels. Package-level
// because the density is: see PxPerMM.
func Px(mm float64) int { return int(math.Round(mm * PxPerMM)) }

// PtPx converts a point size to whole CSS pixels.
func PtPx(pt float64) int { return int(math.Round(pt * PxPerPt)) }
