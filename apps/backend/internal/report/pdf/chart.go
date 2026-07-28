package pdf

import (
	"fmt"
	"strings"

	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/fauzanebd/argentum/internal/report/chart"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// renderChart draws a chart section: an optional title, the image, and a
// caption.
//
// The title and the caption are set in the document's own type rather than
// baked into the bitmap. That is what keeps a chart title the same H2 as every
// other sub-heading, keeps the caption the same muted grey as a table's, and
// keeps both of them selectable and searchable in the finished PDF. It also
// means the deck renderer (T-R4) can take the identical image and put the same
// words on a slide at slide scale.
//
// The whole block is emitted as one unit: a chart split from its caption, or a
// title stranded at the foot of a page above an image on the next, is the same
// orphan problem the table pager solves, and a chart cannot be paged.
func (r *renderer) renderChart(sec spec.Section) error {
	res, err := chart.Render(sec.Chart, chart.Options{
		WidthMM: contentWidth(),
		Format:  r.fmt,
	})
	if err != nil {
		return fmt.Errorf("pdf: chart: %w", err)
	}

	title := strings.TrimSpace(sec.Chart.Title)
	if title == "" {
		title = strings.TrimSpace(sec.Title)
	}
	caption := captionFor(sec, res)

	l := &rowList{}
	l.space(theme.Spacing.SM)
	if title != "" {
		l.text(title, props.Text{
			Family: theme.FontMedium,
			Size:   theme.TypeScale.H2,
			Color:  theme.ColorForeground.Props(),
			Align:  align.Left,
		}, contentWidth())
		l.space(theme.Spacing.XS)
	}

	// Percent 100 with Center false makes maroto scale the PNG to the column
	// width, which is exactly the width it was rendered for — so the image is
	// placed at 1:1 and the 200 DPI it was drawn at is the 200 DPI it prints at.
	l.add(res.HeightMM, col.New(theme.GridCols).Add(
		image.NewFromBytes(res.PNG, extension.Png, props.Rect{
			Percent: 100,
			Center:  false,
		}),
	))

	if caption != "" {
		l.space(theme.Spacing.XS)
		l.text(caption, props.Text{
			Family:          theme.FontBody,
			Style:           fontstyle.Normal,
			Size:            theme.TypeScale.Caption,
			Color:           theme.ColorMuted.Props(),
			Align:           align.Left,
			VerticalPadding: 0.8,
		}, contentWidth())
	}
	l.space(theme.Spacing.SM)

	r.ensure(l.total)
	r.m.AddRows(l.rows...)
	return nil
}

// chartHeightOf is the drawn height of a chart section, for the fit checks that
// run before it is rendered.
func chartHeightOf(sec *spec.Section) float64 {
	if sec == nil || sec.Chart == nil {
		return 0
	}
	return chart.HeightMM(sec.Chart, chart.Options{WidthMM: contentWidth()})
}

// captionFor joins the spec's caption with whatever the renderer had to say
// about the data.
//
// The note is not optional decoration. It is the sentence that tells a reader
// the chart is showing the eight largest of eleven series, and a chart that
// quietly drops three of them without saying so is a chart that misleads by
// omission — the same failure as an axis that does not start at zero, arrived
// at from the other direction.
func captionFor(sec spec.Section, res chart.Result) string {
	parts := make([]string, 0, 2)
	if c := strings.TrimSpace(sec.Caption); c != "" {
		parts = append(parts, c)
	}
	if res.Note != "" {
		parts = append(parts, res.Note)
	}
	return strings.Join(parts, " ")
}
