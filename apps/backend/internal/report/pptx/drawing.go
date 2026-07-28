package pptx

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// DrawingML, written by hand.
//
// There is no layout engine on the other side of this file: OOXML describes
// boxes at absolute positions, and PowerPoint draws exactly what it is told.
// Everything that a page renderer would ask a provider — how tall is this
// paragraph, will this cell fit — is decided here, before a byte is written,
// against the metrics in internal/report/measure.

// fontName is the typeface every run names.
//
// The maroto family keys in the theme (`space-grotesk`, `space-grotesk-medium`)
// are registry keys for an embedded face, not names a copy of PowerPoint can
// look up. OOXML also has no weight axis: a run is bold or it is not, so the
// medium weight the PDF uses for table headers and labels collapses to either
// regular or bold here. Headers take bold; labels take regular.
const fontName = "Space Grotesk"

// pitchFamily is the substitution class declared alongside the typeface: the
// low nibble is variable pitch (2) and the high nibble is the Swiss family (2),
// making 0x22 = 34.
//
// This is as close to a declared fallback chain as OOXML gets. There is no list
// of alternates in the format — the only mechanism that names a second file is
// font embedding, which the ticket rules out because it works in PowerPoint on
// Windows and nowhere else and doubles the file. What `pitchFamily` does is
// tell the consumer what *kind* of face to substitute when it cannot find this
// one, which is why a machine without Space Grotesk falls back to Arial or
// Helvetica or Liberation Sans and not to Times. The width difference between
// those and Space Grotesk is what substitutionMargin pays for.
const pitchFamily = 34

// alignment values as OOXML writes them.
const (
	alignLeft   = "l"
	alignCenter = "ctr"
	alignRight  = "r"
)

// run is a span of text with one set of character properties.
type run struct {
	text  string
	size  float64 // points
	bold  bool
	color theme.Color

	// field, when set, makes this run a live field rather than literal text —
	// "slidenum" is the only one used. The text stays as the cached value, so a
	// consumer that does not evaluate fields still shows the right number.
	field   string
	fieldID string
}

// para is one paragraph: runs, plus how the block is set.
type para struct {
	runs   []run
	align  string
	bullet bool

	// spaceBefore is leading space in points, applied by the consumer rather
	// than by inserting empty paragraphs.
	spaceBefore float64

	// lineSpacing is the multiple of single spacing, e.g. 1.45. Zero takes the
	// consumer's default.
	lineSpacing float64
}

// textBox is a positioned text frame, in millimetres.
type textBox struct {
	x, y, w, h float64
	paras      []para

	// anchor is t|ctr|b: where the text sits inside a box taller than it.
	anchor string

	// inset is the padding inside the frame. Zero means no inset at all, which
	// is what a heading wants — PowerPoint's default 0.1in inset silently
	// shifts every box off the grid it was positioned on.
	inset float64

	// autofit asks the consumer to shrink text that still overflows.
	//
	// The line counts here are estimates against a face the reader may not
	// have, so this is the second line of defence rather than the first: the
	// text has already been chunked and truncated to fit. What it catches is
	// the residual — a substituted face 6% wider than the margin allowed for —
	// and it turns the failure from a clipped line into a slightly smaller one.
	autofit bool

	// name is what the shape is called in PowerPoint's selection pane. Worth
	// setting: "Title 2" is how a person editing the deck finds anything.
	name string
}

// bldr accumulates a slide's XML and hands out the shape ids OOXML requires to
// be unique within a slide.
type bldr struct {
	sb     strings.Builder
	nextID int
}

func newBldr() *bldr {
	// Id 1 belongs to the shape tree's own group, so shapes start at 2.
	return &bldr{nextID: 2}
}

func (b *bldr) id() int {
	id := b.nextID
	b.nextID++
	return id
}

func (b *bldr) printf(format string, args ...any) {
	fmt.Fprintf(&b.sb, format, args...)
}

func (b *bldr) String() string { return b.sb.String() }

// esc XML-escapes s and drops the characters XML cannot carry at all.
//
// The second half matters more than it looks. Every string here came from a
// model, and a stray control byte in a paragraph produces a file that opens
// nowhere and reports "the presentation cannot be opened because part of it is
// missing" — a failure a reader cannot connect to the sentence that caused it.
// Tabs, newlines and carriage returns are legal in XML 1.0; nothing else below
// 0x20 is.
func esc(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '"':
			out.WriteString("&quot;")
		case '\'':
			out.WriteString("&apos;")
		case '\t', '\n', '\r':
			out.WriteRune(' ')
		default:
			if r < 0x20 || r == 0xFFFE || r == 0xFFFF || !unicode.IsGraphic(r) && r != ' ' {
				continue
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}

// xfrm is the position-and-size element every shape carries.
func xfrm(x, y, w, h float64) string {
	return fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`,
		mmToEMU(x), mmToEMU(y), mmToEMU(w), mmToEMU(h))
}

func solidFill(c theme.Color) string {
	return fmt.Sprintf(`<a:solidFill><a:srgbClr val="%s"/></a:solidFill>`, hexRGB(c))
}

// hex is the colour without the leading hash, which is how OOXML writes it.
func hexRGB(c theme.Color) string { return strings.TrimPrefix(c.Hex(), "#") }

// runXML writes one <a:r>, or one <a:fld> when the run is a field.
func runXML(r run) string {
	props := fmt.Sprintf(`<a:rPr lang="en-US" sz="%d" b="%d" dirty="0">%s<a:latin typeface="%s" pitchFamily="%d" charset="0"/><a:cs typeface="%s" pitchFamily="%d" charset="0"/></a:rPr>`,
		ptToHundredths(r.size), boolAttr(r.bold), solidFill(r.color),
		fontName, pitchFamily, fontName, pitchFamily)

	if r.field != "" {
		return fmt.Sprintf(`<a:fld id="%s" type="%s">%s<a:t>%s</a:t></a:fld>`,
			r.fieldID, r.field, props, esc(r.text))
	}
	return fmt.Sprintf(`<a:r>%s<a:t>%s</a:t></a:r>`, props, esc(r.text))
}

func boolAttr(v bool) int {
	if v {
		return 1
	}
	return 0
}

// paraXML writes one <a:p>. An empty paragraph still emits, because that is how
// a blank line between blocks is expressed.
func paraXML(p para) string {
	var sb strings.Builder
	sb.WriteString("<a:p><a:pPr")
	if p.align != "" {
		fmt.Fprintf(&sb, ` algn="%s"`, p.align)
	}
	if p.bullet {
		// The hanging indent is one 0.25in step, which is what PowerPoint's own
		// first outline level uses. Matching it means a bullet pasted into
		// another deck lines up with the bullets already there.
		sb.WriteString(` marL="228600" indent="-228600"`)
	}
	sb.WriteString(">")
	if p.lineSpacing > 0 {
		fmt.Fprintf(&sb, `<a:lnSpc><a:spcPct val="%d"/></a:lnSpc>`, int(p.lineSpacing*100000))
	}
	if p.spaceBefore > 0 {
		fmt.Fprintf(&sb, `<a:spcBef><a:spcPts val="%d"/></a:spcBef>`, ptToHundredths(p.spaceBefore))
	}
	if p.bullet {
		// Space Grotesk has no bullet glyph, and a missing glyph renders as
		// nothing at all — a bulleted list with no bullets. Arial is the one
		// face that is present everywhere this deck can be opened.
		fmt.Fprintf(&sb, `<a:buFont typeface="Arial" pitchFamily="%d" charset="0"/><a:buChar char="•"/>`, pitchFamily)
	} else {
		sb.WriteString(`<a:buNone/>`)
	}
	sb.WriteString("</a:pPr>")
	for _, r := range p.runs {
		sb.WriteString(runXML(r))
	}
	sb.WriteString("</a:p>")
	return sb.String()
}

// bodyPr is the text frame's own properties: insets, anchoring, wrapping and
// autofit.
func bodyPr(tb textBox) string {
	anchor := tb.anchor
	if anchor == "" {
		anchor = "t"
	}
	ins := mmToEMU(tb.inset)
	autofit := "<a:noAutofit/>"
	if tb.autofit {
		autofit = "<a:normAutofit/>"
	}
	return fmt.Sprintf(`<a:bodyPr wrap="square" lIns="%d" tIns="%d" rIns="%d" bIns="%d" anchor="%s">%s</a:bodyPr>`,
		ins, ins, ins, ins, anchor, autofit)
}

// text emits a text frame as a <p:sp> with no fill and no outline.
func (b *bldr) text(tb textBox) {
	id := b.id()
	name := tb.name
	if name == "" {
		name = fmt.Sprintf("TextBox %d", id)
	}
	b.printf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`, id, esc(name))
	b.printf(`<p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>`,
		xfrm(tb.x, tb.y, tb.w, tb.h))
	b.printf(`<p:txBody>%s<a:lstStyle/>`, bodyPr(tb))
	if len(tb.paras) == 0 {
		b.printf(`<a:p><a:endParaRPr lang="en-US"/></a:p>`)
	}
	for _, p := range tb.paras {
		b.printf("%s", paraXML(p))
	}
	b.printf(`</p:txBody></p:sp>`)
}

// rect draws a filled rectangle, optionally rounded and optionally outlined.
// radius is the corner radius in millimetres — RadiusBase, at last, in a format
// that has corners.
func (b *bldr) rect(x, y, w, h float64, fill theme.Color, radius float64, outline *theme.Color) {
	id := b.id()
	geom := `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`
	if radius > 0 {
		// roundRect's adjustment is the radius as a fraction of the shape's
		// shorter side, in hundred-thousandths, capped at half.
		adj := int(radius / min(w, h) * 100000)
		adj = min(max(adj, 0), 50000)
		geom = fmt.Sprintf(`<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val %d"/></a:avLst></a:prstGeom>`, adj)
	}
	ln := `<a:ln><a:noFill/></a:ln>`
	if outline != nil {
		ln = fmt.Sprintf(`<a:ln w="%d">%s</a:ln>`, mmToEMU(theme.Page.Hairline), solidFill(*outline))
	}
	b.printf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Rectangle %d"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>`, id, id)
	b.printf(`<p:spPr>%s%s%s%s</p:spPr>`, xfrm(x, y, w, h), geom, solidFill(fill), ln)
	b.printf(`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>`)
}

// picture places an image already added to the package, referenced by its
// relationship id.
func (b *bldr) picture(relID string, x, y, w, h float64, name string) {
	id := b.id()
	b.printf(`<p:pic><p:nvPicPr><p:cNvPr id="%d" name="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`,
		id, esc(name))
	b.printf(`<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>`, relID)
	b.printf(`<p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`, xfrm(x, y, w, h))
}

// simplePara is the common case: one run, one paragraph.
func simplePara(text string, size float64, bold bool, color theme.Color, align string) para {
	return para{
		runs:        []run{{text: text, size: size, bold: bold, color: color}},
		align:       align,
		lineSpacing: bodyLeading,
	}
}

// wordsFit reports whether s fits in maxLines at the given width — the check
// every chunking decision in deck.go makes.
func wordsFit(s string, size, width float64, maxLines int, bold bool) bool {
	style := measure.Regular
	if bold {
		style = measure.Bold
	}
	return linesIn(s, theme.FontBody, style, size, width) <= maxLines
}
