package pptx

import (
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// A table is drawn as a real DrawingML table (<a:tbl> inside a graphicFrame),
// not as a grid of text boxes.
//
// The difference shows up the moment anyone touches the deck: a real table can
// be selected, sorted, restyled and pasted into another deck as a table, while
// a grid of text boxes is thirty shapes that fall apart when one of them is
// dragged. It also survives the conversion to PDF that the CI smoke test does,
// which a shape grid does only by coincidence.
//
// Every cell carries its own fill and its own rules rather than naming a table
// style. Style inheritance is where the four target applications differ most —
// the same style id renders banded in PowerPoint, flat in Google Slides, and
// with a blue header in some LibreOffice builds — so nothing is inherited.

func (r *renderer) drawTable(b *bldr, s slide) {
	m := s.table
	if m == nil || len(m.Header) == 0 {
		return
	}

	y := bodyTop()
	if m.Caption != "" {
		h := textHeight(m.Caption, theme.FontBody, measure.Regular, deckType.Caption, contentWidth())
		b.text(textBox{
			x: marginX, y: y, w: contentWidth(), h: h, name: "Table Caption",
			paras: []para{simplePara(
				fitLines(m.Caption, theme.FontBody, measure.Regular, deckType.Caption, contentWidth(), 2),
				deckType.Caption, false, theme.ColorMuted, alignLeft)},
		})
		y += h + theme.Spacing.SM
	}

	height := m.HeaderH + float64(len(m.Rows))*m.RowH
	if len(m.Total) > 0 {
		height += m.RowH
	}

	id := b.id()
	b.printf(`<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Table %d"/>`+
		`<p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`,
		id, id)
	b.printf(`<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm>`,
		mmToEMU(marginX), mmToEMU(y), mmToEMU(contentWidth()), mmToEMU(height))
	b.printf(`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table">`)

	// firstRow tells a consumer the top row is a header, which is what makes it
	// repeat when a reader converts the deck to a handout. bandRow is off: the
	// banding is drawn as explicit fills below, and asking for both gives some
	// consumers two bandings at once.
	b.printf(`<a:tbl><a:tblPr firstRow="1" bandRow="0"/><a:tblGrid>`)
	for _, w := range m.Widths {
		b.printf(`<a:gridCol w="%d"/>`, mmToEMU(w))
	}
	b.printf(`</a:tblGrid>`)

	b.printf("%s", tableRow(m.Header, m, m.HeaderH, rowHeader))
	for i, row := range m.Rows {
		style := rowPlain
		if i%2 == 1 {
			style = rowBanded
		}
		b.printf("%s", tableRow(row, m, m.RowH, style))
	}
	if len(m.Total) > 0 {
		b.printf("%s", tableRow(m.Total, m, m.RowH, rowTotal))
	}

	b.printf(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
}

type rowStyle int

const (
	rowPlain rowStyle = iota
	rowBanded
	rowHeader
	rowTotal
)

func tableRow(cells []string, m *tableModel, height float64, style rowStyle) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<a:tr h="%d">`, mmToEMU(height))
	for i := range m.Widths {
		value := ""
		if i < len(cells) {
			value = cells[i]
		}
		sb.WriteString(tableCell(value, m.Aligns[i], m.Size, style))
	}
	sb.WriteString(`</a:tr>`)
	return sb.String()
}

func tableCell(value, align string, size float64, style rowStyle) string {
	var (
		fill = theme.ColorSurface
		bold bool
	)
	switch style {
	case rowHeader:
		// The medium weight the PDF's header uses has no equivalent here —
		// OOXML's only weight axis is bold or not — so the header takes bold.
		fill, bold = theme.ColorSurfaceSubtle, true
	case rowBanded:
		// Zebra bands rather than rules between every row: a band survives a
		// projector, a 0.2mm rule does not.
		fill = theme.ColorSurfaceMuted
	case rowTotal:
		fill, bold = theme.ColorSurfaceSubtle, true
	}

	p := para{
		runs:        []run{{text: value, size: size, bold: bold, color: theme.ColorForeground}},
		align:       align,
		lineSpacing: bodyLeading,
	}

	var sb strings.Builder
	sb.WriteString(`<a:tc><a:txBody><a:bodyPr/><a:lstStyle/>`)
	sb.WriteString(paraXML(p))
	sb.WriteString(`</a:txBody>`)

	// The rule under the header and above the total row is the only chrome the
	// table has. The element order inside tcPr is fixed by the schema — lines
	// before fills — and getting it wrong is one of the ways a deck opens as
	// "repair needed".
	rules := ""
	switch style {
	case rowHeader:
		rules = cellLine("lnB", theme.ColorBorder, theme.Page.Hairline*2)
	case rowTotal:
		rules = cellLine("lnT", theme.ColorBorder, theme.Page.Hairline*3)
	}

	fmt.Fprintf(&sb, `<a:tcPr marL="%d" marR="%d" marT="%d" marB="%d" anchor="ctr">%s%s</a:tcPr></a:tc>`,
		mmToEMU(cellPadX), mmToEMU(cellPadX), mmToEMU(cellPadY), mmToEMU(cellPadY),
		rules, solidFill(fill))
	return sb.String()
}

func cellLine(edge string, color theme.Color, widthMM float64) string {
	return fmt.Sprintf(`<a:%s w="%d" cap="flat" cmpd="sng" algn="ctr">%s<a:prstDash val="solid"/></a:%s>`,
		edge, mmToEMU(widthMM), solidFill(color), edge)
}
