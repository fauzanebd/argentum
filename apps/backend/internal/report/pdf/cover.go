package pdf

import (
	"strings"

	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The cover is the one page in the document with no running chrome, so its
// vertical rhythm has to be built by hand: a top block that starts a fifth of
// the way down, a bottom block pinned to the foot of the page, and whatever is
// left over as space between them.
//
// It is assembled as a measured list of rows rather than added one at a time,
// because the filler in the middle cannot be sized until both ends have been
// measured. If the two blocks together overflow the page the filler goes to
// zero and the page still holds, rather than spilling a stray line onto page 2
// above a running header that does not belong to it.

// coverTitleMaxLines and coverSubtitleMaxLines bound the two blocks that take
// free text from the model. A four-line cover title is already a bad title; a
// twelve-line one would push the rest of the page off the bottom.
const (
	coverTitleMaxLines    = 4
	coverSubtitleMaxLines = 3
)

// rowList is rows plus the running total of their heights.
//
// maroto's Row.GetHeight needs a provider, and the provider does not exist
// until Generate() runs — by which time every layout decision has been made.
// So heights are carried alongside the rows they belong to.
type rowList struct {
	rows  []core.Row
	total float64
}

func (l *rowList) add(height float64, cols ...core.Col) {
	l.rows = append(l.rows, row.New(height).Add(cols...))
	l.total += height
}

func (l *rowList) push(height float64, r core.Row) {
	l.rows = append(l.rows, r)
	l.total += height
}

func (l *rowList) space(height float64) {
	l.add(height, col.New(theme.GridCols))
}

// text adds a full-width row exactly as tall as the wrapped text needs.
func (l *rowList) text(value string, p props.Text, width float64) {
	family, style := p.Family, p.Style
	if family == "" {
		family = theme.FontBody
	}
	if style == "" {
		style = fontstyle.Normal
	}
	h := textHeight(value, family, style, p.Size, width, p)
	l.add(h, col.New(theme.GridCols).Add(text.New(value, p)))
}

func (l *rowList) rule(height float64, p props.Line) {
	l.push(height, line.NewRow(height, p))
}

func (r *renderer) renderCover(sec spec.Section) {
	avail := theme.Page.Height - 2*theme.Page.Margin

	top := r.coverTop(sec)
	bottom := r.coverBottom(sec)

	filler := avail - top.total - bottom.total
	if filler < 0 {
		filler = 0
	}

	r.m.AddRows(top.rows...)
	if filler > 0 {
		r.m.AddRows(row.New(filler).Add(col.New(theme.GridCols)))
	}
	r.m.AddRows(bottom.rows...)
}

func (r *renderer) coverTop(sec spec.Section) *rowList {
	title := firstNonEmpty(sec.Text, sec.Title, r.title, "Report")
	title = truncateToLines(title, theme.FontDisplay, fontstyle.Bold,
		theme.TypeScale.Display, contentWidth(), coverTitleMaxLines)

	l := &rowList{}

	// The mark sits a fifth of the way down rather than at the very top: a
	// cover whose content starts hard against the margin reads as a form.
	const markHeight = 12
	l.space(theme.Spacing.XL * 2)
	l.add(markHeight, r.brandMark(theme.GridCols/2, markHeight, theme.TypeScale.H2))
	l.space(theme.Spacing.XL)

	if period := strings.TrimSpace(sec.Period); period != "" {
		l.text(period, props.Text{
			Family: theme.FontMedium,
			Size:   theme.TypeScale.Caption,
			Color:  theme.ColorPrimary.Props(),
			Align:  align.Left,
		}, contentWidth())
		l.space(theme.Spacing.XS)
	}

	l.text(title, props.Text{
		Family:          theme.FontDisplay,
		Style:           fontstyle.Bold,
		Size:            theme.TypeScale.Display,
		Color:           theme.ColorForeground.Props(),
		Align:           align.Left,
		VerticalPadding: 1.5,
	}, contentWidth())

	if subtitle := strings.TrimSpace(sec.Subtitle); subtitle != "" {
		subtitle = truncateToLines(subtitle, theme.FontBody, fontstyle.Normal,
			theme.TypeScale.H2, contentWidth(), coverSubtitleMaxLines)
		l.space(theme.Spacing.SM)
		l.text(subtitle, props.Text{
			Family: theme.FontBody,
			Size:   theme.TypeScale.H2,
			Color:  theme.ColorMuted.Props(),
			Align:  align.Left,
		}, contentWidth())
	}

	// A short primary rule under the title: the one piece of brand colour on
	// an otherwise typographic page.
	l.space(theme.Spacing.MD)
	l.add(3,
		line.NewCol(30, props.Line{
			Color:       theme.ColorPrimary.Props(),
			Thickness:   1.2,
			SizePercent: 100,
		}),
		col.New(theme.GridCols-30),
	)
	return l
}

func (r *renderer) coverBottom(sec spec.Section) *rowList {
	preparedBy := firstNonEmpty(sec.PreparedBy, r.opts.Brand.Name)

	facts := make([][2]string, 0, 3)
	if v := strings.TrimSpace(sec.PreparedFor); v != "" {
		facts = append(facts, [2]string{r.words.PreparedFor, v})
	}
	if preparedBy != "" {
		facts = append(facts, [2]string{r.words.PreparedBy, preparedBy})
	}
	facts = append(facts, [2]string{r.words.Generated, format.DateTime(r.genAt, r.fmt)})

	labelProps := props.Text{
		Family: theme.FontBody,
		Size:   theme.TypeScale.Caption,
		Color:  theme.ColorMuted.Props(),
		Align:  align.Left,
		Top:    1,
	}
	valueProps := props.Text{
		Family: theme.FontMedium,
		Size:   theme.TypeScale.Body,
		Color:  theme.ColorForeground.Props(),
		Align:  align.Left,
		Top:    1,
	}

	const factHeight = 7
	const labelUnits = 34

	l := &rowList{}
	for _, f := range facts {
		l.add(factHeight,
			col.New(labelUnits).Add(text.New(f[0], labelProps)),
			col.New(theme.GridCols-labelUnits).Add(text.New(f[1], valueProps)),
		)
	}

	if confid := strings.TrimSpace(r.confid); confid != "" {
		l.space(theme.Spacing.MD)
		l.rule(3, props.Line{
			Color:       theme.ColorBorder.Props(),
			Thickness:   theme.Page.Hairline,
			SizePercent: 100,
		})
		l.add(6, col.New(theme.GridCols).Add(
			text.New(strings.ToUpper(confid), props.Text{
				Family: theme.FontMedium,
				Size:   theme.TypeScale.Caption,
				Color:  theme.ColorMuted.Props(),
				Align:  align.Left,
				Top:    1,
			}),
		))
	}
	return l
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
