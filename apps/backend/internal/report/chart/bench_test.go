package chart

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

// fullMeasureOpts is the size a chart is actually drawn at in a report: the
// full 174mm text measure of the PDF, and the 254mm full-bleed width of a deck
// slide. The rest of this file's tests use 90mm on purpose (see idOpts), which
// is a quarter of the pixels — so nothing else here measures what a real render
// costs, and the supersample factor squares into exactly that number.
func fullMeasureOpts(widthMM float64) Options {
	return Options{
		WidthMM:  widthMM,
		HeightMM: 90,
		Format: format.Options{
			Locale:   format.LocaleID,
			Currency: "IDR",
			Decimals: format.AutoDecimals,
		},
	}
}

func benchChart() *spec.Chart {
	months := []string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun"}
	return &spec.Chart{
		Type: spec.ChartGroupedBar, Title: "Direct vs partner", Labels: months, Fmt: "currency",
		Series: []spec.Series{
			{Name: "Direct", Values: []float64{412_000_000, 448_000_000, 391_000_000, 520_000_000, 604_000_000, 588_000_000}},
			{Name: "Partner", Values: []float64{210_000_000, 233_000_000, 268_000_000, 251_000_000, 305_000_000, 342_000_000}},
		},
	}
}

// BenchmarkRenderFullMeasure exists to hold `supersample` honest.
//
// The factor squares into the canvas — 3 rasterises nine times the final pixel
// area, 2 rasterises four — and every one of those pixels is allocated more than
// once: by the rasteriser, again decoding the PNG the library hands back, again
// as the CatmullRom destination. That is a cost no test in this package could
// see, because every other render here is 90mm.
//
// Read `B/op`, not `ns/op`. The reason the factor moved is a worker's memory
// limit, not its clock.
func BenchmarkRenderFullMeasure(b *testing.B) {
	for _, tc := range []struct {
		name    string
		widthMM float64
	}{
		{"pdf_174mm", 174},
		{"deck_254mm", 254},
	} {
		b.Run(tc.name, func(b *testing.B) {
			c, opts := benchChart(), fullMeasureOpts(tc.widthMM)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Render(c, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
