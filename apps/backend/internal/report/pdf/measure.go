package pdf

import (
	"fmt"
	"strings"
	"sync"

	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// A layout engine needs to know how wide a string is before it decides how
// wide a column should be, and maroto does not expose that: its provider is
// internal and only exists once Generate() is running, by which time every
// layout decision has already been made.
//
// So this file keeps a second gofpdf document that draws nothing. It is
// constructed exactly the way maroto constructs its own — millimetres, A4, the
// same embedded faces (see maroto's internal/providers/gofpdf/builder.go) — so
// GetStringWidth returns the same number the real renderer will use, not an
// approximation of it. Guessing here would show up as columns that are 5% too
// narrow and text that clips, which is the failure the risk register calls out
// by name.
//
// The document is package-level and immutable after construction: measuring
// only reads font metrics. It is guarded by a mutex because SetFont mutates
// the shared gofpdf state and two turns can render at once in the worker.
var (
	measureOnce sync.Once
	measurePDF  *gofpdf.Fpdf
	measureErr  error
	measureMu   sync.Mutex
)

// gofpdf writes its font catalogue in Go map order, so the same spec rendered
// twice produces the same pages with the font objects numbered differently —
// different bytes, no golden test, and a diff between two builds that says
// nothing. SetDefaultCatalogSort is the library's own switch for this: it
// sorts the resource catalogues instead, and every Fpdf created afterwards
// inherits it, including the one maroto builds inside itself where there is no
// other way to reach it.
//
// It is a package-level global in gofpdf, which is why this is an init and not
// a call site. Nothing else in this process uses gofpdf directly, and sorting a
// catalogue changes nothing a reader can see.
func init() { gofpdf.SetDefaultCatalogSort(true) }

func measurer() (*gofpdf.Fpdf, error) {
	measureOnce.Do(func() {
		fonts, err := theme.CustomFonts()
		if err != nil {
			measureErr = err
			return
		}
		p := gofpdf.NewCustom(&gofpdf.InitType{
			OrientationStr: "P",
			UnitStr:        "mm",
			Size:           gofpdf.SizeType{Wd: theme.Page.Width, Ht: theme.Page.Height},
		})
		for _, f := range fonts {
			p.AddUTF8FontFromBytes(f.Family, string(f.Style), f.Bytes)
		}
		p.SetMargins(theme.Page.Margin, theme.Page.Margin, theme.Page.Margin)
		p.AddPage()
		if err := p.Error(); err != nil {
			measureErr = fmt.Errorf("pdf: measurer: %w", err)
			return
		}
		measurePDF = p
	})
	return measurePDF, measureErr
}

// textWidth is the rendered width of s in millimetres.
func textWidth(s string, family string, style fontstyle.Type, size float64) float64 {
	p, err := measurer()
	if err != nil || p == nil {
		// Fall back to a crude estimate rather than failing a render over a
		// column width. 0.5em per rune is close enough for Space Grotesk that
		// a table still reads; it is not close enough to rely on, which is why
		// the font check in bootstrap exists.
		return float64(len([]rune(s))) * size * 0.5 * mmPerPoint
	}
	measureMu.Lock()
	defer measureMu.Unlock()
	p.SetFont(family, string(style), size)
	return p.GetStringWidth(s)
}

// mmPerPoint converts a type size to a line height. maroto's font provider
// divides the point size by its scale factor (72/25.4) to get millimetres, so
// one line of 10pt text is 3.53mm tall — not the 1.2× leading a typesetter
// would expect. Matching maroto exactly matters more than matching typography:
// a row whose height disagrees with maroto's clips its own text.
const mmPerPoint = 25.4 / 72.0

// lineHeight is the height of one line of text at a given point size, in mm.
func lineHeight(sizePt float64) float64 { return sizePt * mmPerPoint }

// wrapLines splits s the way maroto will, given a column width in millimetres.
//
// This is a transcription of getLinesBreakingLineFromSpace in
// internal/providers/gofpdf/text.go. It is duplicated rather than called
// because it is unexported there, and it is transcribed rather than
// approximated because the two must agree: if this returns 2 where maroto
// renders 3, the third line is drawn outside its row and lands on top of the
// next one.
func wrapLines(s string, family string, style fontstyle.Type, size, colWidth float64) []string {
	p, err := measurer()
	if err != nil || p == nil || colWidth <= 0 {
		return []string{s}
	}
	measureMu.Lock()
	defer measureMu.Unlock()
	p.SetFont(family, string(style), size)

	var (
		lines   []string
		current float64
	)
	for _, word := range strings.Split(s, " ") {
		if word == "" {
			continue
		}
		piece, separator := word, ""
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			piece, separator = " "+word, " "
		}
		width := p.GetStringWidth(piece)
		if current+width <= colWidth {
			if len(lines) == 0 {
				lines = append(lines, "")
			}
			lines[len(lines)-1] += separator + word
			current += width
		} else {
			lines = append(lines, word)
			current = p.GetStringWidth(word)
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// textHeight is the height a text component will occupy, matching
// text.Text.GetHeight: one font height per line, plus the vertical padding
// between them, plus the top and bottom offsets.
func textHeight(s string, family string, style fontstyle.Type, size, colWidth float64, p props.Text) float64 {
	lines := len(wrapLines(s, family, style, size, colWidth-p.Left-p.Right))
	h := float64(lines)*lineHeight(size) + float64(lines-1)*p.VerticalPadding
	return h + p.Top + p.Bottom
}

// truncateToLines shortens s until it wraps into at most maxLines, appending an
// ellipsis when it had to cut.
//
// PowerPoint's renderer silently clips overflow and T-R4 treats that as an
// acceptance failure; the same standard applies here. A visible "…" tells the
// reader the cell was longer than the column. A cell that simply stops
// mid-word tells them nothing, and a cell drawn over the row beneath it tells
// them something false.
func truncateToLines(s string, family string, style fontstyle.Type, size, colWidth float64, maxLines int) string {
	if maxLines <= 0 {
		return s
	}
	lines := wrapLines(s, family, style, size, colWidth)
	if len(lines) <= maxLines {
		return s
	}
	kept := strings.Join(lines[:maxLines], " ")
	runes := []rune(kept)
	for len(runes) > 1 {
		candidate := strings.TrimRight(string(runes), " ") + "…"
		if len(wrapLines(candidate, family, style, size, colWidth)) <= maxLines {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

// fitText is truncateToLines plus the case it cannot handle: a token with no
// spaces in it.
//
// maroto breaks lines at spaces and nowhere else, so "SO-2026-4100" in a 20mm
// column is one line 26mm wide, drawn straight over the column to its right.
// Line-count truncation never fires because the text is already one line. The
// 200-row export fixture is where this shows up, and it shows up as an order
// number printed across a customer name.
//
// So each wrapped line is measured, and any line still wider than the column
// is cut by characters until it fits with an ellipsis. Breaking mid-token is
// not pretty; a cell bleeding into its neighbour is not readable.
func fitText(s string, family string, style fontstyle.Type, size, colWidth float64, maxLines int) string {
	s = truncateToLines(s, family, style, size, colWidth, maxLines)
	lines := wrapLines(s, family, style, size, colWidth)

	changed := false
	for i, ln := range lines {
		if textWidth(ln, family, style, size) <= colWidth {
			continue
		}
		lines[i] = clipRunes(ln, family, style, size, colWidth)
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(lines, " ")
}

// clipRunes cuts a single line to width, ending in an ellipsis.
func clipRunes(s string, family string, style fontstyle.Type, size, colWidth float64) string {
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := strings.TrimRight(string(runes), " ") + "…"
		if textWidth(candidate, family, style, size) <= colWidth {
			return candidate
		}
	}
	return "…"
}
