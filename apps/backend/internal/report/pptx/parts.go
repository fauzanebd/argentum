package pptx

import (
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The parts of the package that do not depend on the document's content: the
// theme, the slide master, the one layout, the notes master, and the two
// property files.
//
// They are generated rather than committed as template files for the same
// reason the Go theme is generated: every colour and every typeface in here
// comes from tokens.json, and a committed XML file with #F25C5C typed into it
// is a fourth copy of the design system waiting to drift from the other three.

// themeXML is the DrawingML theme.
//
// A deck without one does not open. Most of it is the format's required
// furniture — three fill styles, three line styles, three effect styles, three
// background fills, in that order and in that number — and none of that is
// visible to a reader, because every shape this renderer draws names its own
// colour explicitly. What matters is the two parts that are ours: the colour
// scheme, so a recipient recolouring the deck through PowerPoint's own theme
// picker gets Argentum's palette, and the font scheme, so the fallback applies
// to text this renderer did not write.
func themeXML(name string) string {
	accents := make([]string, 6)
	for i := range accents {
		// The theme carries six accents and the chart palette has eight; the
		// first six are the ones a reader can recolour a shape with, and the
		// last two are only ever used by the chart images, which are rasters
		// and take their colours from theme.ChartPalette directly.
		accents[i] = fmt.Sprintf(`<a:accent%d><a:srgbClr val="%s"/></a:accent%d>`,
			i+1, hexRGB(theme.SeriesColor(i)), i+1)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, `<a:theme xmlns:a="%s" name="%s">`, nsA, esc(name))
	sb.WriteString(`<a:themeElements>`)

	fmt.Fprintf(&sb, `<a:clrScheme name="%s">`, esc(name))
	fmt.Fprintf(&sb, `<a:dk1><a:srgbClr val="%s"/></a:dk1>`, hexRGB(theme.ColorForeground))
	fmt.Fprintf(&sb, `<a:lt1><a:srgbClr val="%s"/></a:lt1>`, hexRGB(theme.ColorSurface))
	fmt.Fprintf(&sb, `<a:dk2><a:srgbClr val="%s"/></a:dk2>`, hexRGB(theme.ColorInfo))
	fmt.Fprintf(&sb, `<a:lt2><a:srgbClr val="%s"/></a:lt2>`, hexRGB(theme.ColorBackground))
	sb.WriteString(strings.Join(accents, ""))
	fmt.Fprintf(&sb, `<a:hlink><a:srgbClr val="%s"/></a:hlink>`, hexRGB(theme.ColorInfo))
	fmt.Fprintf(&sb, `<a:folHlink><a:srgbClr val="%s"/></a:folHlink>`, hexRGB(theme.ColorMuted))
	sb.WriteString(`</a:clrScheme>`)

	font := fmt.Sprintf(`<a:latin typeface="%s" pitchFamily="%d" charset="0"/><a:ea typeface=""/><a:cs typeface=""/>`,
		fontName, pitchFamily)
	fmt.Fprintf(&sb, `<a:fontScheme name="%s"><a:majorFont>%s</a:majorFont><a:minorFont>%s</a:minorFont></a:fontScheme>`,
		esc(name), font, font)

	sb.WriteString(fmtSchemeXML)
	sb.WriteString(`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`)
	return sb.String()
}

// fmtSchemeXML is the format scheme: flat fills, hairline strokes, no effects.
//
// Office's default scheme is full of gradients and soft shadows from 2007. A
// shape a recipient inserts into this deck picks up whichever scheme is here,
// so leaving the default in would mean a box they draw does not match a box the
// renderer drew. The counts are fixed by the schema — exactly three of each.
const fmtSchemeXML = `<a:fmtScheme name="Argentum">` +
	`<a:fillStyleLst>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`</a:fillStyleLst>` +
	`<a:lnStyleLst>` +
	`<a:ln w="6350" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln>` +
	`<a:ln w="12700" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln>` +
	`<a:ln w="19050" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln>` +
	`</a:lnStyleLst>` +
	`<a:effectStyleLst>` +
	`<a:effectStyle><a:effectLst/></a:effectStyle>` +
	`<a:effectStyle><a:effectLst/></a:effectStyle>` +
	`<a:effectStyle><a:effectLst/></a:effectStyle>` +
	`</a:effectStyleLst>` +
	`<a:bgFillStyleLst>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`</a:bgFillStyleLst>` +
	`</a:fmtScheme>`

// emptyShapeTree is the minimum a <p:spTree> may contain: the group shape's own
// identity and its (unused) child transform. Every slide, master and layout
// starts with it.
const emptyShapeTree = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/>` +
	`<a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

// colourMap ties the six abstract slots every part refers to (bg1, tx1, …) to
// the theme's colour scheme. It is written identically on the master and the
// notes master; slides inherit it through <a:masterClrMapping/>.
const colourMap = `<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" ` +
	`accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`

// slideMasterXML is the master every slide layout hangs off.
//
// It is deliberately empty of shapes. This renderer positions everything
// absolutely and inherits no placeholders, because placeholder inheritance is
// where the four target applications disagree most: the same empty title
// placeholder is drawn by PowerPoint, ignored by Google Slides and given a
// default outline by some LibreOffice builds. A master with nothing on it
// cannot be interpreted three ways.
func slideMasterXML() string {
	return fmt.Sprintf(`<p:sldMaster xmlns:a="%s" xmlns:r="%s" xmlns:p="%s">`+
		`<p:cSld><p:bg><p:bgPr>%s<a:effectLst/></p:bgPr></p:bg><p:spTree>%s</p:spTree></p:cSld>`+
		`%s`+
		`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>`+
		`<p:txStyles>%s</p:txStyles>`+
		`</p:sldMaster>`,
		nsA, nsR, nsP, solidFill(theme.ColorBackground), emptyShapeTree, colourMap, txStylesXML())
}

// txStylesXML gives the master's three text styles a defined size and family,
// so a text box a recipient adds to the deck starts inside the design system
// rather than at Calibri 18.
func txStylesXML() string {
	style := func(tag string, size float64, color theme.Color) string {
		return fmt.Sprintf(`<p:%s><a:lvl1pPr><a:defRPr sz="%d">%s<a:latin typeface="%s" pitchFamily="%d" charset="0"/></a:defRPr></a:lvl1pPr></p:%s>`,
			tag, ptToHundredths(size), solidFill(color), fontName, pitchFamily, tag)
	}
	return style("titleStyle", deckType.H1, theme.ColorForeground) +
		style("bodyStyle", deckType.Body, theme.ColorForeground) +
		style("otherStyle", deckType.Body, theme.ColorForeground)
}

// slideLayoutXML is the single blank layout. One layout, because every slide
// this renderer produces is drawn shape by shape — a layout set would be seven
// more parts describing placeholders nothing uses.
func slideLayoutXML() string {
	return fmt.Sprintf(`<p:sldLayout xmlns:a="%s" xmlns:r="%s" xmlns:p="%s" type="blank" preserve="1">`+
		`<p:cSld name="Blank"><p:spTree>%s</p:spTree></p:cSld>`+
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`,
		nsA, nsR, nsP, emptyShapeTree)
}

// notesMasterXML is what the speaker-notes pages inherit from. The body
// placeholder has to exist here for the notes text on each slide to have
// something to be positioned by; its geometry is the lower two-thirds of a
// portrait Letter page, which is where PowerPoint puts it.
func notesMasterXML() string {
	const (
		notesBodyX = 685800
		notesBodyY = 4343400
		notesBodyW = 5486400
		notesBodyH = 4114800
	)
	body := fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Notes Placeholder 1"/>`+
		`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr>`+
		`<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
		`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>`+
		`<p:txBody><a:bodyPr wrap="square"/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>`,
		notesBodyX, notesBodyY, notesBodyW, notesBodyH)

	notesStyle := fmt.Sprintf(`<p:notesStyle><a:lvl1pPr><a:defRPr sz="1200">%s<a:latin typeface="%s" pitchFamily="%d" charset="0"/></a:defRPr></a:lvl1pPr></p:notesStyle>`,
		solidFill(theme.ColorForeground), fontName, pitchFamily)

	return fmt.Sprintf(`<p:notesMaster xmlns:a="%s" xmlns:r="%s" xmlns:p="%s">`+
		`<p:cSld><p:spTree>%s%s</p:spTree></p:cSld>%s%s</p:notesMaster>`,
		nsA, nsR, nsP, emptyShapeTree, body, colourMap, notesStyle)
}

// presPropsXML and tableStylesXML are required parts with nothing to say. The
// table style list is empty on purpose: every table cell this renderer writes
// carries its own fill and its own rules, so there is no style for a consumer
// to apply and no default GUID for it to fail to find.
const (
	presPropsXML   = `<p:presentationPr xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"/>`
	tableStylesXML = `<a:tblStyleLst xmlns:a="` + nsA + `" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`
)

// presentationXML lists the masters and the slides, in order, and states the
// slide size. The element order is fixed by the schema and is not negotiable:
// masters, notes master, slide list, slide size, notes size.
func presentationXML(slideRelIDs []string) string {
	var slides strings.Builder
	for i, id := range slideRelIDs {
		// Slide ids are the deck's own identifiers and must be at least 256.
		fmt.Fprintf(&slides, `<p:sldId id="%d" r:id="%s"/>`, 256+i, id)
	}
	return fmt.Sprintf(`<p:presentation xmlns:a="%s" xmlns:r="%s" xmlns:p="%s" saveSubsetFonts="1">`+
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`+
		`<p:notesMasterIdLst><p:notesMasterId r:id="rId2"/></p:notesMasterIdLst>`+
		`<p:sldIdLst>%s</p:sldIdLst>`+
		`<p:sldSz cx="%d" cy="%d"/><p:notesSz cx="%d" cy="%d"/>`+
		`</p:presentation>`,
		nsA, nsR, nsP, slides.String(),
		slideWidthEMU, slideHeightEMU, notesWidthEMU, notesHeightEMU)
}

// corePropsXML is what a records system files the deck under. Both timestamps
// are the document's generated_at rather than the clock, for the same reason
// the PDF pins /CreationDate and /ModDate: two renders of one spec have to
// produce one file.
func corePropsXML(title, author, subject, keywords string, generated time.Time) string {
	stamp := generated.UTC().Format("2006-01-02T15:04:05Z")
	var sb strings.Builder
	sb.WriteString(`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	// The schema is a sequence, so this order is required rather than tidy.
	fmt.Fprintf(&sb, `<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>`, stamp)
	if author != "" {
		fmt.Fprintf(&sb, `<dc:creator>%s</dc:creator>`, esc(author))
	}
	if keywords != "" {
		fmt.Fprintf(&sb, `<cp:keywords>%s</cp:keywords>`, esc(keywords))
	}
	if author != "" {
		fmt.Fprintf(&sb, `<cp:lastModifiedBy>%s</cp:lastModifiedBy>`, esc(author))
	}
	fmt.Fprintf(&sb, `<dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>`, stamp)
	if subject != "" {
		fmt.Fprintf(&sb, `<dc:subject>%s</dc:subject>`, esc(subject))
	}
	fmt.Fprintf(&sb, `<dc:title>%s</dc:title>`, esc(title))
	sb.WriteString(`</cp:coreProperties>`)
	return sb.String()
}

func appPropsXML(company string, slides, notes int) string {
	var sb strings.Builder
	sb.WriteString(`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" ` +
		`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">`)
	if company != "" {
		fmt.Fprintf(&sb, `<Company>%s</Company>`, esc(company))
	}
	fmt.Fprintf(&sb, `<PresentationFormat>Widescreen</PresentationFormat>`)
	fmt.Fprintf(&sb, `<Slides>%d</Slides><Notes>%d</Notes>`, slides, notes)
	sb.WriteString(`<Application>Argentum</Application></Properties>`)
	return sb.String()
}
