package pptx

import (
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Drawing a slide.
//
// The deck has three grounds, and which one a slide sits on carries meaning
// rather than decoration: the cover and the section dividers are dark, so the
// deck has a visible rhythm when the slide sorter is open; content slides are
// the cream the report is printed on; and nothing else varies. A deck where
// every slide has its own background is a deck assembled from templates.

// slideXML writes one slide. imageRel is the relationship id of the chart image
// declared in this slide's own rels part, empty when the slide has no picture;
// logoRel is the same for the tenant's mark in the footer strip.
func (r *renderer) slideXML(s slide, number int, imageRel, logoRel string) string {
	b := newBldr()

	dark := s.kind == kindCover || s.kind == kindDivider || s.kind == kindClosing
	bg := theme.ColorBackground
	if dark {
		bg = theme.ColorForeground
	}

	switch s.kind {
	case kindCover:
		r.drawCover(b, s)
	case kindDivider:
		r.drawDivider(b, s)
	case kindClosing:
		r.drawClosing(b, s)
	case kindKPI:
		r.drawTitle(b, s)
		r.drawKPI(b, s)
		r.drawFooter(b, s, number, logoRel)
	case kindChart:
		r.drawTitle(b, s)
		r.drawChart(b, s, imageRel)
		r.drawFooter(b, s, number, logoRel)
	case kindTable:
		r.drawTitle(b, s)
		r.drawTable(b, s)
		r.drawFooter(b, s, number, logoRel)
	case kindFacts:
		r.drawTitle(b, s)
		r.drawFacts(b, s)
		r.drawFooter(b, s, number, logoRel)
	case kindBullets:
		r.drawTitle(b, s)
		r.drawBullets(b, s)
		r.drawFooter(b, s, number, logoRel)
	}

	return fmt.Sprintf(`<p:sld xmlns:a="%s" xmlns:r="%s" xmlns:p="%s">`+
		`<p:cSld><p:bg><p:bgPr>%s<a:effectLst/></p:bgPr></p:bg><p:spTree>%s%s</p:spTree></p:cSld>`+
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`,
		nsA, nsR, nsP, solidFill(bg), emptyShapeTree, b.String())
}

// titleText is the slide title with its continuation marker, if it has one.
func (r *renderer) titleText(s slide) string {
	title := strings.TrimSpace(s.title)
	if s.continued {
		title = strings.TrimSpace(title + " " + r.words.Continued)
	}
	return title
}

// drawTitle is the title band on every content slide: the title, truncated to
// two lines, and the short brand rule under it.
func (r *renderer) drawTitle(b *bldr, s slide) {
	title := r.titleText(s)
	if title == "" {
		return
	}
	title = fitLines(title, theme.FontBody, measure.Bold, deckType.H1, contentWidth(), 2)

	b.text(textBox{
		x: marginX, y: marginTop, w: contentWidth(), h: titleBand,
		anchor: "b", autofit: true, name: "Title",
		paras: []para{simplePara(title, deckType.H1, true, theme.ColorForeground, alignLeft)},
	})
	b.rect(marginX, marginTop+titleBand+theme.Spacing.XS, titleRuleWidth, titleRuleThickness,
		r.accent(), 0, nil)
}

// logoBand is the drawn height of the tenant's mark in the footer strip. The
// strip is footerBand tall and the mark sits inside it, so this is smaller: a
// logo that fills the strip touches the hairline above it and the slide edge
// below.
const logoBand = 5.0

// logoMaxWidth caps how far a very wide mark may run into the footer text. A
// wordmark logo can be 8:1, and without a cap it would take a third of the
// strip and push the confidentiality label off it.
const logoMaxWidth = 32.0

// drawFooter is the strip at the foot of a content slide: the tenant's mark and
// the confidentiality label on the left, the slide number on the right.
func (r *renderer) drawFooter(b *bldr, s slide, number int, logoRel string) {
	left := strings.TrimSpace(r.confid)
	if note := strings.TrimSpace(r.opts.Brand.FooterNote); note != "" {
		if left != "" {
			left += " · " + note
		} else {
			left = note
		}
	}
	if left == "" {
		left = strings.TrimSpace(r.opts.Brand.Name)
	}
	if !r.opts.Brand.HideCredit {
		if left != "" {
			left += " · " + r.words.Credit
		} else {
			left = r.words.Credit
		}
	}

	b.rect(marginX, footerTop(), contentWidth(), theme.Page.Hairline, theme.ColorBorder, 0, nil)

	textX := marginX
	textWidth := contentWidth() * 0.7
	if logoRel != "" && r.logoAspect > 0 {
		w, h := logoBand*r.logoAspect, logoBand
		if w > logoMaxWidth {
			// Clamping the width has to shrink the height with it. Capping one
			// dimension alone is exactly how a wordmark ends up squashed, and
			// a distorted logo is worse than no logo.
			w, h = logoMaxWidth, logoMaxWidth/r.logoAspect
		}
		b.picture(logoRel, marginX, footerTop()+theme.Spacing.XS, w, h, "Logo")
		textX += w + theme.Spacing.SM
		textWidth -= w + theme.Spacing.SM
	}

	if left != "" && textWidth > 0 {
		b.text(textBox{
			x: textX, y: footerTop() + theme.Spacing.XS, w: textWidth, h: footerBand,
			name: "Footer",
			paras: []para{simplePara(fitLines(left, theme.FontBody, measure.Regular, deckType.Caption, textWidth, 1),
				deckType.Caption, false, theme.ColorMuted, alignLeft)},
		})
	}

	// A live field rather than a literal, so a deck someone reorders renumbers
	// itself. The cached text is the right number either way, which is what a
	// consumer that does not evaluate fields will show.
	b.text(textBox{
		x: marginX + contentWidth()*0.7, y: footerTop() + theme.Spacing.XS, w: contentWidth() * 0.3, h: footerBand,
		name: "Slide Number",
		paras: []para{{
			align: alignRight,
			runs: []run{{
				text:    fmt.Sprintf("%d", number),
				size:    deckType.Caption,
				color:   theme.ColorMuted,
				field:   "slidenum",
				fieldID: fieldGUID(number),
			}},
		}},
	})
}

// fieldGUID is the identifier OOXML requires on a field. It has to be a GUID
// and it has to be stable across renders, so it is derived from the slide
// number rather than generated — a random one would make the same spec produce
// different bytes on every run.
func fieldGUID(n int) string {
	return fmt.Sprintf("{A9C1D4E2-0000-4000-8000-%012X}", n)
}

// The cover's vertical grid.
//
// Every band is a fixed position and a fixed height, and the text inside it is
// anchored to the edge the next band abuts — the title to the bottom of its
// band, the subtitle to the top of its own. Nothing is positioned relative to
// how tall the block above it turned out to be.
//
// That is not tidiness, it is the fix for a defect this renderer shipped once:
// the cover chained its offsets off estimated text heights, and a subtitle the
// estimate put on one line came back from LibreOffice on two, with the brand
// rule drawn straight through the second. An estimate that is one line out is a
// certainty over enough documents — the face is substituted, after all — so the
// layout is built so that being one line out costs nothing.
const (
	coverBrandTop     = 20.0
	coverPeriodTop    = 46.0
	coverTitleTop     = 56.0
	coverTitleBand    = 45.0 // two lines of Display
	coverSubtitleBand = 26.0 // two lines of H2
	coverBandGap      = 3.0
	coverFactsHeight  = 26.0

	// coverTitleMaxLines and coverSubtitleMaxLines are what the bands hold. A
	// three-line cover title is already a bad title.
	coverTitleMaxLines    = 2
	coverSubtitleMaxLines = 2
)

func (r *renderer) drawCover(b *bldr, s slide) {
	if name := firstNonEmpty(r.opts.Brand.Name, "Argentum"); name != "" {
		b.text(textBox{
			x: marginX, y: coverBrandTop, w: contentWidth(), h: 14, name: "Brand",
			paras: []para{simplePara(name, deckType.H2, true, r.accentOn(theme.ColorForeground), alignLeft)},
		})
	}

	if s.period != "" {
		b.text(textBox{
			x: marginX, y: coverPeriodTop, w: contentWidth(), h: 10, name: "Period",
			paras: []para{simplePara(strings.ToUpper(s.period), deckType.Caption, false,
				r.accentOn(theme.ColorForeground), alignLeft)},
		})
	}

	title := fitLines(s.title, theme.FontBody, measure.Bold, deckType.Display, contentWidth(), coverTitleMaxLines)
	b.text(textBox{
		x: marginX, y: coverTitleTop, w: contentWidth(), h: coverTitleBand,
		anchor: "b", autofit: true, name: "Cover Title",
		paras: []para{simplePara(title, deckType.Display, true, theme.ColorBackground, alignLeft)},
	})

	y := coverTitleTop + coverTitleBand + coverBandGap
	if s.subtitle != "" {
		sub := fitLines(s.subtitle, theme.FontBody, measure.Regular, deckType.H2,
			contentWidth()*0.8, coverSubtitleMaxLines)
		b.text(textBox{
			x: marginX, y: y, w: contentWidth() * 0.8, h: coverSubtitleBand,
			autofit: true, name: "Cover Subtitle",
			paras: []para{simplePara(sub, deckType.H2, false, theme.ColorBorder, alignLeft)},
		})
		y += coverSubtitleBand
	}

	b.rect(marginX, y+coverBandGap, titleRuleWidth*1.4, titleRuleThickness*1.5,
		r.accentOn(theme.ColorForeground), 0, nil)

	r.drawFactStrip(b, s, slideHeightMM-marginBottom-coverFactsHeight)
}

// drawFactStrip is the prepared-for / prepared-by / generated block that sits at
// the foot of the cover and the closing slide.
func (r *renderer) drawFactStrip(b *bldr, s slide, y float64) {
	if len(s.facts) == 0 {
		return
	}
	width := contentWidth() / float64(len(s.facts))
	for i, f := range s.facts {
		x := marginX + float64(i)*width
		b.text(textBox{
			x: x, y: y, w: width - theme.Spacing.MD, h: 18, name: "Cover Fact",
			paras: []para{
				simplePara(f[0], deckType.Caption, false, theme.ColorMuted, alignLeft),
				simplePara(fitLines(f[1], theme.FontBody, measure.Bold, deckType.Body, width-theme.Spacing.MD, 1),
					deckType.Body, true, theme.ColorBackground, alignLeft),
			},
		})
	}
	if s.confidentiality != "" {
		b.text(textBox{
			x: marginX, y: slideHeightMM - marginBottom - 6, w: contentWidth(), h: 8, name: "Confidentiality",
			paras: []para{simplePara(strings.ToUpper(s.confidentiality), deckType.Caption, false,
				theme.ColorMuted, alignLeft)},
		})
	}
}

// The divider and the closing slide centre a fixed band rather than a measured
// one, for the reason the cover's grid gives: the band is the same height
// whether the title came back on one line or two, so the rule above it does not
// move and cannot land on the text.
func (r *renderer) drawDivider(b *bldr, s slide) {
	title := fitLines(s.title, theme.FontBody, measure.Bold, deckType.Display, contentWidth(), coverTitleMaxLines)
	y := (slideHeightMM - coverTitleBand) / 2

	b.rect(marginX, y-theme.Spacing.LG, titleRuleWidth, titleRuleThickness*1.5,
		r.accentOn(theme.ColorForeground), 0, nil)
	b.text(textBox{
		x: marginX, y: y, w: contentWidth(), h: coverTitleBand,
		anchor: "ctr", autofit: true, name: "Section Title",
		paras: []para{simplePara(title, deckType.Display, true, theme.ColorBackground, alignLeft)},
	})
}

// closingTitleBand is two lines of H1.
const closingTitleBand = 30.0

func (r *renderer) drawClosing(b *bldr, s slide) {
	title := fitLines(firstNonEmpty(s.title, "Argentum"), theme.FontBody, measure.Bold,
		deckType.H1, contentWidth(), 2)
	y := slideHeightMM/2 - closingTitleBand

	b.rect(marginX, y-theme.Spacing.LG, titleRuleWidth, titleRuleThickness*1.5,
		r.accentOn(theme.ColorForeground), 0, nil)
	b.text(textBox{
		x: marginX, y: y, w: contentWidth(), h: closingTitleBand,
		anchor: "ctr", autofit: true, name: "Closing Title",
		paras: []para{simplePara(title, deckType.H1, true, theme.ColorBackground, alignLeft)},
	})
	r.drawFactStrip(b, s, slideHeightMM-marginBottom-coverFactsHeight)
}

// kpiCardGap is the space between two KPI tiles.
const kpiCardGap = 8.0

func (r *renderer) drawKPI(b *bldr, s slide) {
	n := len(s.items)
	if n == 0 {
		return
	}
	width := (contentWidth() - float64(n-1)*kpiCardGap) / float64(n)
	height := min(bodyHeight()*0.62, 62.0)
	y := bodyTop() + (bodyHeight()-height)/2

	for i, item := range s.items {
		x := marginX + float64(i)*(width+kpiCardGap)
		border := theme.ColorBorder
		b.rect(x, y, width, height, theme.ColorSurface, theme.RadiusBase*1.6, &border)

		inner := width - 2*theme.Spacing.MD
		label := fitLines(item.KeyText(), theme.FontBody, measure.Regular, deckType.Caption, inner, 2)

		// KPI values are compact by default: "Rp 3,86 Miliar" fits a tile,
		// "Rp 3.863.405.700" does not — and the exact figure belongs in the
		// table the tile is summarising, not on the tile.
		valueFmt := r.fmt
		valueFmt.Compact = true
		value := canvas.CellText(item.ValueCell(), format.KindNumber, valueFmt)

		// Step the value down the type scale rather than letting it wrap. A
		// tile whose number is a point smaller than its neighbour's still reads
		// as a row of tiles; one whose number wraps does not.
		valueSize := deckType.Display
		for _, size := range []float64{deckType.Display, deckType.H1, deckType.H2} {
			valueSize = size
			if measure.Width(value, theme.FontBody, measure.Bold, size) <= inner*substitutionMargin {
				break
			}
		}

		paras := []para{
			simplePara(label, deckType.Caption, false, theme.ColorMuted, alignLeft),
			{
				runs:        []run{{text: value, size: valueSize, bold: true, color: theme.ColorForeground}},
				align:       alignLeft,
				spaceBefore: 6,
				lineSpacing: 1.1,
			},
		}
		if item.DeltaPct != nil {
			deltaColor := theme.ColorDestructive
			if item.GoodDirection() {
				deltaColor = theme.ColorPositive
			}
			// ↑ and ↓ rather than ▲ and ▼: Space Grotesk has the arrows and not
			// the triangles, and a missing glyph renders as nothing at all.
			arrow := "↓"
			if item.Rising() {
				arrow = "↑"
			}
			deltaOpts := r.fmt
			deltaOpts.Decimals = 1
			paras = append(paras, para{
				runs: []run{{
					text:  arrow + " " + format.Signed(*item.DeltaPct, deltaOpts),
					size:  deckType.Caption,
					bold:  true,
					color: deltaColor,
				}},
				align:       alignLeft,
				spaceBefore: 5,
				lineSpacing: 1.1,
			})
		}

		b.text(textBox{
			x: x + theme.Spacing.MD, y: y + theme.Spacing.MD,
			w: inner, h: height - 2*theme.Spacing.MD,
			anchor: "ctr", autofit: true, name: "KPI",
			paras: paras,
		})
	}
}

func (r *renderer) drawChart(b *bldr, s slide, imageRel string) {
	if s.chart == nil || imageRel == "" {
		return
	}
	c := s.chart
	captionH := 0.0
	if s.subtitle != "" {
		captionH = textHeight(s.subtitle, theme.FontBody, measure.Regular, deckType.Caption, contentWidth()) + theme.Spacing.SM
	}

	// The image is placed at exactly the size it was rasterised for, centred in
	// what is left of the body area. Scaling it here would throw away the
	// resolution it was rendered at, which is the whole reason it is rendered
	// at slide size in the first place.
	y := bodyTop() + max(0, (bodyHeight()-captionH-c.heightMM)/2)
	x := marginX + max(0, (contentWidth()-c.widthMM)/2)
	b.picture(imageRel, x, y, c.widthMM, c.heightMM, "Chart")

	if s.subtitle != "" {
		b.text(textBox{
			x: marginX, y: y + c.heightMM + theme.Spacing.SM, w: contentWidth(), h: captionH,
			name:  "Caption",
			paras: []para{simplePara(s.subtitle, deckType.Caption, false, theme.ColorMuted, alignLeft)},
		})
	}
}

func (r *renderer) drawFacts(b *bldr, s slide) {
	y := bodyTop()
	labelW := contentWidth() * factLabelShare
	valueW := contentWidth() - labelW

	for _, f := range s.facts {
		h := factRowHeight(f)
		b.text(textBox{
			x: marginX, y: y, w: labelW - theme.Spacing.SM, h: h, name: "Fact Label",
			paras: []para{simplePara(
				fitLines(f[0], theme.FontBody, measure.Regular, deckType.Body, labelW-theme.Spacing.SM, 1),
				deckType.Body, false, theme.ColorMuted, alignLeft)},
		})
		b.text(textBox{
			x: marginX + labelW, y: y, w: valueW, h: h, autofit: true, name: "Fact Value",
			paras: []para{simplePara(
				fitLines(f[1], theme.FontBody, measure.Bold, deckType.Body, valueW, maxFactLines),
				deckType.Body, true, theme.ColorForeground, alignLeft)},
		})
		y += h
	}
}

func (r *renderer) drawBullets(b *bldr, s slide) {
	y := bodyTop()
	lineH := measure.LineHeightMM(deckType.Body) * bodyLeading

	if len(s.bullets) > 0 {
		paras := make([]para, 0, len(s.bullets))
		height := 0.0
		for i, bl := range s.bullets {
			p := para{
				runs:        []run{{text: bl.text, size: deckType.Body, color: theme.ColorForeground}},
				align:       alignLeft,
				bullet:      true,
				lineSpacing: bodyLeading,
			}
			if i > 0 {
				p.spaceBefore = 12
			}
			paras = append(paras, p)
			height += float64(max(bl.lines, 1))*lineH + theme.Spacing.MD
		}
		b.text(textBox{
			x: marginX, y: y, w: contentWidth(), h: height, autofit: true, name: "Bullets",
			paras: paras,
		})
		y += height + theme.Spacing.MD
	}

	if s.callout != nil {
		h := calloutHeight(s.callout)
		// The callout is pinned to the foot of the body area when there is room
		// above it, so a slide with one bullet and one callout does not have
		// the callout floating in the middle of the page.
		if bottom := bodyTop() + bodyHeight() - h; bottom > y {
			y = bottom
		}
		r.drawCallout(b, *s.callout, y, h)
	}
}

// calloutAccentWidth is the coloured spine on the left of a callout — the same
// device the PDF uses, where maroto had no corner radius to work with. Here the
// box is genuinely rounded and the spine stays anyway, because the tone has to
// survive being projected through a beamer that flattens colour.
const calloutAccentWidth = 3.0

func calloutHeight(c *calloutBox) float64 {
	inner := contentWidth() - calloutAccentWidth - 2*theme.Spacing.LG
	h := 2 * theme.Spacing.MD
	if c.title != "" {
		h += textHeight(c.title, theme.FontBody, measure.Bold, deckType.H2, inner)
	}
	if c.text != "" {
		h += textHeight(c.text, theme.FontBody, measure.Regular, deckType.Body, inner)
	}
	return h
}

func (r *renderer) drawCallout(b *bldr, c calloutBox, y, h float64) {
	accent := toneColor(c.tone)
	b.rect(marginX, y, contentWidth(), h, accent.Tint(0.88), theme.RadiusBase*1.6, nil)
	b.rect(marginX, y, calloutAccentWidth, h, accent, 0, nil)

	inner := contentWidth() - calloutAccentWidth - 2*theme.Spacing.LG
	paras := make([]para, 0, 2)
	if c.title != "" {
		paras = append(paras, simplePara(
			fitLines(c.title, theme.FontBody, measure.Bold, deckType.H2, inner, 2),
			deckType.H2, true, theme.ColorForeground, alignLeft))
	}
	if c.text != "" {
		p := simplePara(fitLines(c.text, theme.FontBody, measure.Regular, deckType.Body, inner, 4),
			deckType.Body, false, theme.ColorForeground, alignLeft)
		if len(paras) > 0 {
			p.spaceBefore = 6
		}
		paras = append(paras, p)
	}
	b.text(textBox{
		x: marginX + calloutAccentWidth + theme.Spacing.LG, y: y + theme.Spacing.MD,
		w: inner, h: h - 2*theme.Spacing.MD,
		anchor: "ctr", autofit: true, name: "Callout",
		paras: paras,
	})
}

// toneColor maps a callout tone to a semantic colour. The tones deliberately do
// not all resolve to the brand red: a warning and a good-news box that are the
// same colour communicate nothing.
func toneColor(tone string) theme.Color {
	switch tone {
	case spec.ToneWarn:
		return theme.ColorWarning
	case spec.ToneGood:
		return theme.ColorPositive
	default:
		return theme.ColorInfo
	}
}
