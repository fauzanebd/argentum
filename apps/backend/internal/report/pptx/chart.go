package pptx

import (
	"fmt"
	"math"
	"strings"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/chart"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

// chartModel is a rendered chart and the space it takes.
type chartModel struct {
	png      []byte
	widthMM  float64
	heightMM float64
}

// maxChartHeight is the tallest a chart may be drawn on a slide: the body area
// less the caption line under it. Shared with the video renderer since T-V1.
func maxChartHeight() float64 { return surface.MaxChartHeight() }

// chart draws the chart at slide scale and puts it on a slide of its own.
//
// It is rasterised for the deck rather than re-used from the PDF. The two are
// the same drawing of the same numbers, from the same package, on the same
// palette — but a 174mm-wide image stretched across 291mm of slide is a 120 DPI
// image on a projector, and the acceptance criterion is that charts appear at
// slide resolution without visible artefacts. What must not vary between the
// two is the chart's content, and that is guaranteed by both going through
// chart.Render rather than by sharing a buffer.
func (b *builder) Chart(title string, sec spec.Section) error {
	width := contentWidth()
	height := math.Min(maxChartHeight(), width*chartAspect)

	res, err := chart.Render(sec.Chart, chart.Options{
		WidthMM:  width,
		HeightMM: height,
		Format:   b.r.fmt,
	})
	if err != nil {
		return fmt.Errorf("pptx: chart: %w", err)
	}

	caption := strings.TrimSpace(sec.Caption)
	if res.Note != "" {
		caption = strings.TrimSpace(caption + " " + res.Note)
	}

	b.slides = append(b.slides, slide{
		kind:     kindChart,
		title:    title,
		subtitle: caption,
		chart: &chartModel{
			png:      res.PNG,
			widthMM:  res.WidthMM,
			heightMM: res.HeightMM,
		},
	})
	return nil
}

// chartAspect is canvas.ChartAspect under the name this file has always used.
const chartAspect = canvas.ChartAspect
