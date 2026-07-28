package chart

import (
	"fmt"

	"github.com/fauzanebd/argentum/internal/report/format"
)

// The words this package contributes to a document it did not write: the bucket
// a truncated series or category is folded into, and the one state a chart can
// be in that is not a chart.
//
// Same rule as the PDF renderer's labels (see pdf/labels.go): they follow the
// document's locale, not the process's, and they exist only because they are
// the strings the renderer chooses rather than the model. This is not i18n and
// should not grow into one.
type labels struct {
	other  string
	noData string

	// The three below are the sentences appended to a caption when a cap
	// fired. They are sentences and not counts because the reader of the
	// document, not the caller, is who has to understand that the chart is not
	// showing everything. Dropped and bucketed are different sentences because
	// they are different facts: one chart is missing data, the other is
	// summarising it.
	seriesDropped      func(shown, total int) string
	seriesBucketed     func(shown, total int) string
	categoriesBucketed func(shown, total int) string
}

func labelsFor(loc format.Locale) labels {
	if loc == format.LocaleID {
		return labels{
			other:  "Lainnya",
			noData: "Tidak ada data untuk periode ini",
			seriesDropped: func(shown, total int) string {
				return fmt.Sprintf("Menampilkan %d seri terbesar dari %d; sisanya tidak digambar.", shown, total)
			},
			seriesBucketed: func(shown, total int) string {
				return fmt.Sprintf("Menampilkan %d seri terbesar dari %d; sisanya digabung sebagai Lainnya.", shown, total)
			},
			categoriesBucketed: func(shown, total int) string {
				return fmt.Sprintf("Menampilkan %d kategori terbesar dari %d; sisanya digabung sebagai Lainnya.", shown, total)
			},
		}
	}
	return labels{
		other:  "Other",
		noData: "No data for this period",
		seriesDropped: func(shown, total int) string {
			return fmt.Sprintf("Showing the %d largest of %d series; the rest are not plotted.", shown, total)
		},
		seriesBucketed: func(shown, total int) string {
			return fmt.Sprintf("Showing the %d largest of %d series; the rest are grouped as Other.", shown, total)
		},
		categoriesBucketed: func(shown, total int) string {
			return fmt.Sprintf("Showing the %d largest of %d categories; the rest are grouped as Other.", shown, total)
		},
	}
}
