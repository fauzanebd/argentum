package pdf

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// bodyLeading is the multiple of the font height a line of body copy occupies.
//
// maroto stacks lines at exactly the point size converted to millimetres,
// which is the height of the glyphs and not the height of a line — set solid,
// 10pt on 10pt. Every paragraph in this renderer is emitted a line at a time
// precisely so this can be applied, and so a page break can land between two
// lines of a paragraph instead of pushing the whole block to the next page.
const bodyLeading = 1.32

func (r *renderer) renderSection(sec spec.Section, next *spec.Section) error {
	switch sec.Type {
	case spec.SectionCover:
		return nil // drawn by run(), or ignored in v1
	case spec.SectionHeading:
		r.renderHeading(sec, next)
	case spec.SectionParagraph:
		r.renderParagraph(sec.Text)
	case spec.SectionFootnote:
		r.renderFootnote(sec)
	case spec.SectionKeyValue:
		r.renderKeyValue(sec)
	case spec.SectionKPIRow:
		r.renderKPIRow(sec)
	case spec.SectionCallout:
		r.renderCallout(sec)
	case spec.SectionPromo:
		// A page cannot draw a promotion card either, and here the
		// degradation is a KPI row rather than a callout: two prices side by
		// side is exactly what a KPI row is for, and it is the one shape on a
		// page that puts two figures next to each other and expects the
		// reader to compare them (T-G12).
		r.renderKPIRow(promoAsKPIRow(sec))
	case spec.SectionHero:
		// A page has no hero: the format's whole grammar is a heading with
		// content under it, and a headline filling A4 is a poster (T-G11).
		// The callout is the prominent block this renderer does have — an
		// accent rule, a title and a line of body — so a hero written for a
		// social image and also asked for as a PDF says the same words in the
		// same order. The alternative is the `default` below, which fails the
		// whole document over a section type the model was invited to write.
		r.renderCallout(heroAsCallout(sec))
	case spec.SectionTable:
		return r.renderTable(sec.Table())
	case spec.SectionChart:
		return r.renderChart(sec)
	case spec.SectionPageBreak:
		r.breakPage()
	case spec.SectionSpacer:
		size := sec.Size
		if size <= 0 {
			size = theme.Spacing.SM
		}
		r.space(size)
	default:
		return fmt.Errorf("pdf: unknown section type %q", sec.Type)
	}
	return nil
}

// renderHeading draws a numbered section heading. Level 1 gets a primary rule
// under it; level 2 does not, because two ruled levels stop reading as a
// hierarchy.
//
// Numbering only happens in v2, and only when there is more than one top-level
// section. A v1 spec that has been producing "Line Items" for three months
// should not start producing "1. Line Items" because the backend was upgraded.
func (r *renderer) renderHeading(sec spec.Section, next *spec.Section) {
	level := sec.Level
	if level != 2 {
		level = 1
	}
	label := strings.TrimSpace(sec.Text)
	if r.numbered {
		label = r.number(level) + " " + label
	}

	l := &rowList{}
	if level == 1 {
		l.space(theme.Spacing.LG)
		l.text(label, props.Text{
			Family:          theme.FontDisplay,
			Style:           fontstyle.Bold,
			Size:            theme.TypeScale.H1,
			Color:           theme.ColorForeground.Props(),
			Align:           align.Left,
			VerticalPadding: 1,
		}, contentWidth())
		l.space(theme.Spacing.XS)
		l.add(2.5,
			line.NewCol(24, props.Line{
				Color:       r.accent().Props(),
				Thickness:   0.8,
				SizePercent: 100,
			}),
			col.New(theme.GridCols-24),
		)
		l.space(theme.Spacing.SM)
	} else {
		l.space(theme.Spacing.MD)
		l.text(label, props.Text{
			Family: theme.FontMedium,
			Size:   theme.TypeScale.H2,
			Color:  theme.ColorForeground.Props(),
			Align:  align.Left,
		}, contentWidth())
		l.space(theme.Spacing.XS)
	}

	// A heading alone at the foot of a page is the classic orphan, and "two
	// lines of body copy" is not a wide enough reservation: the thing that
	// most often follows a heading in a report is a row of KPI cards, which
	// is 23mm and indivisible. So the check asks what actually comes next.
	r.ensure(l.total + followHeight(next))
	r.m.AddRows(l.rows...)
}

// followHeight is how much of the next section has to fit beside a heading for
// the heading not to be stranded. It is the first indivisible piece of that
// section, not the whole of it — a table may legitimately start on this page
// and continue on the next; it may not start on the next page while its
// heading stays on this one.
func followHeight(next *spec.Section) float64 {
	body := lineHeight(theme.TypeScale.Body) * bodyLeading
	if next == nil {
		return 0
	}
	switch next.Type {
	case spec.SectionKPIRow:
		return kpiCardHeight + theme.Spacing.SM
	case spec.SectionTable:
		return theme.Page.TableHeaderHeight + theme.Page.TableRowHeight
	case spec.SectionChart:
		// A chart is indivisible, so the whole of it has to fit beside its
		// heading — unlike a table, it cannot start here and continue overleaf.
		return chartHeightOf(next) + 2*theme.Spacing.SM
	case spec.SectionCallout:
		return 2*theme.Spacing.SM + 2*body
	case spec.SectionHeading, spec.SectionPageBreak:
		// Two headings in a row: the second one runs this check itself.
		return 0
	default:
		return 2 * body
	}
}

// number advances and renders the section counter: "1." at level 1, "1.1" at
// level 2.
func (r *renderer) number(level int) string {
	if level == 1 {
		r.h1++
		r.h2 = 0
		return strconv.Itoa(r.h1) + "."
	}
	if r.h1 == 0 {
		r.h1 = 1
	}
	r.h2++
	return strconv.Itoa(r.h1) + "." + strconv.Itoa(r.h2)
}

// renderParagraph emits justified body copy one line at a time.
//
// One row per line looks wasteful and is the opposite: maroto cannot split a
// row, so a paragraph emitted as a single row is a block that either fits on
// the current page or moves to the next one whole, leaving a hole. Emitting
// lines lets the page break fall between them, which is what a paragraph is
// supposed to do.
//
// The last line is left-aligned. maroto justifies whatever it is given and its
// guard against over-stretched spaces only fires past ten times the normal
// space width, so a justified final line of three words would be spread across
// the full measure — the single most recognisable mark of a document laid out
// by something that does not read.
func (r *renderer) renderParagraph(body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	lines := wrapLines(body, theme.FontBody, fontstyle.Normal, theme.TypeScale.Body, contentWidth())
	lh := lineHeight(theme.TypeScale.Body)
	rowH := lh * bodyLeading

	r.space(theme.Spacing.XS)
	for i, ln := range lines {
		// align.Justify is declared untyped in maroto while the rest of the
		// package is align.Type, so the conversion is not decoration.
		alignment := align.Type(align.Justify)
		if i == len(lines)-1 {
			alignment = align.Left
		}
		r.m.AddRow(rowH, col.New(theme.GridCols).Add(
			text.New(ln, props.Text{
				Family: theme.FontBody,
				Size:   theme.TypeScale.Body,
				Color:  theme.ColorForeground.Props(),
				Align:  alignment,
				Top:    (rowH - lh) / 2,
			}),
		))
	}
	r.space(theme.Spacing.XS)
}

// renderFootnote is the source or methodology line under a table or chart:
// small, muted, and never separated from what it annotates.
func (r *renderer) renderFootnote(sec spec.Section) {
	body := strings.TrimSpace(sec.Text)
	if body == "" {
		return
	}
	l := &rowList{}
	l.space(theme.Spacing.XS)
	l.text(body, props.Text{
		Family:          theme.FontBody,
		Size:            theme.TypeScale.Caption,
		Color:           theme.ColorMuted.Props(),
		Align:           align.Left,
		VerticalPadding: 0.8,
	}, contentWidth())
	r.ensure(l.total)
	r.m.AddRows(l.rows...)
}

// renderKeyValue is the invoice/agreement header block: a label column and a
// value column, one pair per row.
func (r *renderer) renderKeyValue(sec spec.Section) {
	const labelUnits = 34
	labelProps := props.Text{
		Family: theme.FontMedium,
		Size:   theme.TypeScale.Body,
		Color:  theme.ColorMuted.Props(),
		Align:  align.Left,
	}
	valueProps := props.Text{
		Family: theme.FontBody,
		Size:   theme.TypeScale.Body,
		Color:  theme.ColorForeground.Props(),
		Align:  align.Left,
	}

	r.space(theme.Spacing.XS)
	for _, item := range sec.Items {
		key := item.KeyText()
		value := r.cellText(item.ValueCell(), format.KindText, r.fmt)

		// The row is as tall as the taller of the two columns, so a long
		// address in the value column does not overprint the row beneath it.
		lh := lineHeight(theme.TypeScale.Body)
		keyLines := len(wrapLines(key, theme.FontMedium, fontstyle.Normal, theme.TypeScale.Body, colWidth(labelUnits)-2))
		valLines := len(wrapLines(value, theme.FontBody, fontstyle.Normal, theme.TypeScale.Body, colWidth(theme.GridCols-labelUnits)))
		lines := max(keyLines, valLines)
		rowH := float64(lines)*lh*bodyLeading + 1

		kp, vp := labelProps, valueProps
		kp.Top = (lh*bodyLeading - lh) / 2
		vp.Top = kp.Top
		kp.Right = 2

		r.ensure(rowH)
		r.m.AddRow(rowH,
			col.New(labelUnits).Add(text.New(key, kp)),
			col.New(theme.GridCols-labelUnits).Add(text.New(value, vp)),
		)
	}
	r.space(theme.Spacing.XS)
}

// kpiCardHeight is fixed so a row of cards reads as a row. The three text
// baselines below are offsets inside it.
const (
	kpiCardHeight = 23.0
	kpiGapUnits   = 3
)

// renderKPIRow draws 2-4 cards across the measure.
//
// The whole row is one maroto row: a column can hold several components, each
// positioned by its own Top offset, so a card is one bordered cell with three
// baselines in it rather than three stacked rows pretending to be a card.
func (r *renderer) renderKPIRow(sec spec.Section) {
	items := sec.Items
	if len(items) == 0 {
		return
	}
	if len(items) > 4 {
		items = items[:4]
	}

	n := len(items)
	gaps := (n - 1) * kpiGapUnits
	cardUnits := (theme.GridCols - gaps) / n
	// The remainder goes to the last card rather than being dropped, so the
	// row still ends flush with the right margin.
	lastUnits := theme.GridCols - gaps - cardUnits*(n-1)

	cardStyle := &props.Cell{
		BackgroundColor: theme.ColorSurface.Props(),
		BorderColor:     theme.ColorBorder.Props(),
		BorderType:      border.Full,
		BorderThickness: theme.Page.Hairline,
	}

	cols := make([]core.Col, 0, 2*n)
	for i, item := range items {
		units := cardUnits
		if i == n-1 {
			units = lastUnits
		}
		if i > 0 {
			cols = append(cols, col.New(kpiGapUnits))
		}
		cols = append(cols, r.kpiCard(item, units).WithStyle(cardStyle))
	}

	r.space(theme.Spacing.XS)
	r.ensure(kpiCardHeight + theme.Spacing.SM)
	r.m.AddRow(kpiCardHeight, cols...)
	r.space(theme.Spacing.SM)
}

func (r *renderer) kpiCard(item spec.Item, units int) core.Col {
	inner := colWidth(units) - 2*theme.Spacing.XS

	label := truncateToLines(item.KeyText(), theme.FontMedium, fontstyle.Normal,
		theme.TypeScale.Caption, inner, 1)

	// KPI values are compact by default: "Rp 3,86 Miliar" fits a card,
	// "Rp 3.863.405.700" does not — and the exact figure belongs in the table
	// the card is summarising, not in the card.
	valueFmt := r.fmt
	valueFmt.Compact = true
	value := r.cellText(item.ValueCell(), format.KindNumber, valueFmt)

	// Step the value down the type scale rather than letting it wrap or clip.
	// A card whose number is a point smaller than its neighbour's still reads
	// as a row of cards; one whose number is cut in half does not.
	valueSize := theme.TypeScale.H1
	for _, size := range []float64{theme.TypeScale.H1, theme.TypeScale.H2, theme.TypeScale.Body} {
		valueSize = size
		if textWidth(value, theme.FontDisplay, fontstyle.Bold, size) <= inner {
			break
		}
	}

	components := []core.Component{
		text.New(label, props.Text{
			Family: theme.FontMedium,
			Size:   theme.TypeScale.Caption,
			Color:  theme.ColorMuted.Props(),
			Align:  align.Left,
			Left:   theme.Spacing.XS,
			Right:  theme.Spacing.XS,
			Top:    3,
		}),
		text.New(value, props.Text{
			Family: theme.FontDisplay,
			Style:  fontstyle.Bold,
			Size:   valueSize,
			Color:  theme.ColorForeground.Props(),
			Align:  align.Left,
			Left:   theme.Spacing.XS,
			Right:  theme.Spacing.XS,
			Top:    8,
		}),
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
		components = append(components, text.New(
			arrow+" "+format.Signed(*item.DeltaPct, deltaOpts),
			props.Text{
				Family: theme.FontMedium,
				Size:   theme.TypeScale.Caption,
				Color:  deltaColor.Props(),
				Align:  align.Left,
				Left:   theme.Spacing.XS,
				Right:  theme.Spacing.XS,
				Top:    16,
			}))
	}

	return col.New(units).Add(components...)
}

// toneColor maps a callout tone to a semantic colour. The tones deliberately do
// not all resolve to the brand red: a warning and a good-news box that are the
// same colour communicate nothing.
//
// These come from the semantic tokens rather than from ChartPalette, which is
// where they used to be indexed from. A categorical ramp is ordered by
// separability, not by meaning, so `ChartPalette[7]` meant "good" only for as
// long as the eighth series happened to be green — which T-R3 ended when the
// colour-vision gate replaced it with an azure.
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

// renderCallout draws a tinted box with a coloured spine.
//
// The spec calls for rounded corners; maroto draws rectangles and has no
// radius, so the corners are square and the box earns its shape from the
// 2-unit accent bar on its left instead. RadiusBase stays in the theme for the
// deck renderer (T-R4), where OOXML does have a corner radius.
// promoAsKPIRow is the promotion a page can draw: the price before and the
// price now, as two cards under the product's name.
//
// The labels are deliberately plain rather than the badge's words. "CRAZY
// DEAL" is a shelf-edge device and reads as noise in a document; what a
// reader of a PDF needs is which figure is which.
func promoAsKPIRow(sec spec.Section) spec.Section {
	items := make([]spec.Item, 0, 2)
	if sec.Was != nil {
		items = append(items, spec.Item{Label: "Before", Value: sec.Was})
	}
	if sec.Price != nil {
		label := "Now"
		if unit := strings.TrimSpace(sec.Unit); unit != "" {
			label += " " + unit
		}
		items = append(items, spec.Item{Label: label, Value: sec.Price})
	}
	return spec.Section{Type: spec.SectionKPIRow, Title: strings.TrimSpace(sec.Title), Items: items}
}

// heroAsCallout is the hero a page can draw: the headline as the callout's
// title, the supporting line as its body, and the kicker prefixed to the
// title because a callout has nowhere else to put it.
func heroAsCallout(sec spec.Section) spec.Section {
	headline := strings.TrimSpace(sec.Title)
	body := strings.TrimSpace(sec.Text)
	if headline == "" {
		headline, body = body, ""
	}
	if kicker := strings.TrimSpace(sec.Subtitle); kicker != "" && headline != "" {
		headline = kicker + " — " + headline
	}
	return spec.Section{Type: spec.SectionCallout, Title: headline, Text: body, Tone: sec.Tone}
}

func (r *renderer) renderCallout(sec spec.Section) {
	const accentUnits = 2
	bodyUnits := theme.GridCols - accentUnits
	inner := colWidth(bodyUnits) - 2*theme.Spacing.SM

	accent := toneColor(sec.Tone)
	title := strings.TrimSpace(sec.Title)
	body := strings.TrimSpace(sec.Text)

	lh := lineHeight(theme.TypeScale.Body)
	height := theme.Spacing.SM
	var components []core.Component

	if title != "" {
		title = truncateToLines(title, theme.FontMedium, fontstyle.Normal, theme.TypeScale.Body, inner, 2)
		lines := len(wrapLines(title, theme.FontMedium, fontstyle.Normal, theme.TypeScale.Body, inner))
		components = append(components, text.New(title, props.Text{
			Family:          theme.FontMedium,
			Size:            theme.TypeScale.Body,
			Color:           theme.ColorForeground.Props(),
			Align:           align.Left,
			Left:            theme.Spacing.SM,
			Right:           theme.Spacing.SM,
			Top:             height - lh*0.15,
			VerticalPadding: lh * (bodyLeading - 1),
		}))
		height += float64(lines) * lh * bodyLeading
	}
	if body != "" {
		lines := len(wrapLines(body, theme.FontBody, fontstyle.Normal, theme.TypeScale.Body, inner))
		components = append(components, text.New(body, props.Text{
			Family:          theme.FontBody,
			Size:            theme.TypeScale.Body,
			Color:           theme.ColorForeground.Props(),
			Align:           align.Left,
			Left:            theme.Spacing.SM,
			Right:           theme.Spacing.SM,
			Top:             height - lh*0.15,
			VerticalPadding: lh * (bodyLeading - 1),
		}))
		height += float64(lines) * lh * bodyLeading
	}
	height += theme.Spacing.SM

	r.space(theme.Spacing.SM)
	r.ensure(height + theme.Spacing.SM)
	r.m.AddRow(height,
		col.New(accentUnits).WithStyle(&props.Cell{BackgroundColor: accent.Props()}),
		col.New(bodyUnits).
			WithStyle(&props.Cell{BackgroundColor: accent.Tint(0.9).Props()}).
			Add(components...),
	)
	r.space(theme.Spacing.SM)
}

// cellText formats one spec cell. The cell's own fmt wins over the column's,
// which is how a total row states its currency in a column of plain numbers.
func (r *renderer) cellText(c spec.Cell, fallback format.Kind, opts format.Options) string {
	kind := fallback
	if c.Fmt != "" {
		kind = format.ParseKind(c.Fmt)
	}
	return format.Value(c.V, kind, opts)
}
