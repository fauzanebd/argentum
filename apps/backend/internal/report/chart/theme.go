package chart

import (
	"fmt"
	"image/color"
	"sync"

	"github.com/go-analyze/charts"
	"github.com/golang/freetype/truetype"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The charts library carries its own themes and its own font (Roboto). Both are
// replaced here, because a chart that does not match the document it sits in is
// the exact failure the design-token package exists to prevent — one that is
// invisible in code review and obvious on the page.

// chartFontFamily is the key the charts library registers Space Grotesk under.
// It is a separate registry from maroto's, hence a second name for the same
// bytes.
const chartFontFamily = "argentum-space-grotesk"

var (
	fontOnce sync.Once
	fontFace *truetype.Font
	fontErr  error
)

// chartFont installs Space Grotesk into the charts library's font registry and
// returns the parsed face.
//
// Medium rather than Regular: chart labels are small, they sit on a coloured
// ground as often as not, and at 8pt scaled to print resolution the regular
// weight goes thin enough to break up on a laser printer. The dashboard makes
// the same choice for axis labels.
func chartFont() (*truetype.Font, error) {
	fontOnce.Do(func() {
		data, err := theme.FontBytes(theme.FontMedium)
		if err != nil {
			fontErr = err
			return
		}
		if err := charts.InstallFont(chartFontFamily, data); err != nil {
			fontErr = fmt.Errorf("chart: install font: %w", err)
			return
		}
		fontFace = charts.GetFont(chartFontFamily)
		if fontFace == nil {
			fontErr = fmt.Errorf("chart: font %q did not register", chartFontFamily)
		}
	})
	return fontFace, fontErr
}

// rgba converts a theme colour to the library's. Documents are print artifacts
// and composite nothing, so alpha is always opaque — see theme.Color.
func rgba(c theme.Color) charts.Color {
	return charts.Color{R: c.R, G: c.G, B: c.B, A: 0xFF}
}

// nrgba is the same colour for the Go image packages, used by the no-data state
// and the downscaler.
func nrgba(c theme.Color) color.NRGBA {
	return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 0xFF}
}

// palette builds the library's ColorPalette from the design tokens.
//
// Every colour here is a token. The one judgement the function makes is which
// token plays which role, and the roles are chosen to match the document: axis
// rules are the same hairline grey as a table rule, labels are the same muted
// grey as a caption, and the series ramp is ChartPalette in its own order —
// which is what makes series 1 on a chart the same red as the rule under a
// heading.
func palette() charts.ColorPalette {
	series := make([]charts.Color, len(theme.ChartPalette))
	for i, c := range theme.ChartPalette {
		series[i] = rgba(c)
	}
	return charts.MakeTheme(charts.ThemeOption{
		IsDarkMode:      false,
		BackgroundColor: rgba(theme.ColorSurface),
		AxisStrokeColor: rgba(theme.ColorBorder),
		// The split lines are the horizontal rules a reader's eye follows from
		// the axis to the bar. They are a tint of the border rather than the
		// border itself: at full strength forty of them out-weigh the data.
		AxisSplitLineColor: rgba(theme.ColorBorder.Tint(0.45)),
		TextColor:          rgba(theme.ColorMuted),
		TextColorTitle:     rgba(theme.ColorForeground),
		TextColorLabel:     rgba(theme.ColorForeground),
		TextColorLegend:    rgba(theme.ColorForeground),
		TextColorXAxis:     rgba(theme.ColorMuted),
		TextColorYAxis:     rgba(theme.ColorMuted),
		SeriesColors:       series,
	})
}
