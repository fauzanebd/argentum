// Package measure answers the question every layout decision in this tree
// starts from: how wide is this string, set in this face, at this size.
//
// It exists as its own package because two renderers now need the answer and
// they must get the same one. The PDF pager and the deck's table slides both
// decide column widths by measuring content; if they measured differently, the
// same table would be proportioned one way in the report and another way in the
// deck attached to it — the self-disagreement the chart package was built to
// avoid, arrived at through typography instead of through data.
//
// The measurement is real, not estimated. maroto does not expose its own text
// provider — it is internal and only exists once Generate() is running, by which
// time every layout decision has already been made — so this package keeps a
// second gofpdf document that draws nothing, constructed exactly the way maroto
// constructs its own (millimetres, A4, the same embedded faces; see maroto's
// internal/providers/gofpdf/builder.go). GetStringWidth then returns the number
// the real renderer will use rather than an approximation of it. Guessing here
// shows up as columns that are 5% too narrow and text that clips, which is the
// failure the risk register calls out by name.
package measure

import (
	"fmt"
	"strings"
	"sync"

	"github.com/phpdave11/gofpdf"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Style is a face within a family. The values are gofpdf's own style codes,
// which are also maroto's, so a caller holding a maroto fontstyle.Type converts
// with a plain cast rather than a lookup table.
type Style string

const (
	Regular Style = ""
	Bold    Style = "B"
	Italic  Style = "I"
)

// The document is package-level and immutable after construction: measuring
// only reads font metrics. It is guarded by a mutex because SetFont mutates the
// shared gofpdf state and two turns can render at once in the worker.
var (
	once sync.Once
	doc  *gofpdf.Fpdf
	err  error
	mu   sync.Mutex
)

func measurer() (*gofpdf.Fpdf, error) {
	once.Do(func() {
		fonts, ferr := theme.CustomFonts()
		if ferr != nil {
			err = ferr
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
		if perr := p.Error(); perr != nil {
			err = fmt.Errorf("measure: %w", perr)
			return
		}
		doc = p
	})
	return doc, err
}

// MMPerPoint converts a type size to a line height.
//
// maroto's font provider divides the point size by its scale factor (72/25.4)
// to get millimetres, so one line of 10pt text is 3.53mm tall — not the 1.2×
// leading a typesetter would expect. Matching maroto exactly matters more than
// matching typography: a row whose height disagrees with maroto's clips its own
// text. Callers that want leading apply it themselves.
const MMPerPoint = 25.4 / 72.0

// LineHeightMM is the height of one line of text at a given point size, in mm.
func LineHeightMM(sizePt float64) float64 { return sizePt * MMPerPoint }

// Width is the rendered width of s in millimetres.
func Width(s string, family string, style Style, sizePt float64) float64 {
	p, merr := measurer()
	if merr != nil || p == nil {
		// Fall back to a crude estimate rather than failing a render over a
		// column width. 0.5em per rune is close enough for Space Grotesk that a
		// table still reads; it is not close enough to rely on, which is why the
		// font check in bootstrap exists.
		return float64(len([]rune(s))) * sizePt * 0.5 * MMPerPoint
	}
	mu.Lock()
	defer mu.Unlock()
	p.SetFont(family, string(style), sizePt)
	return p.GetStringWidth(s)
}

// Wrap splits s the way maroto will, given a column width in millimetres.
//
// This is a transcription of getLinesBreakingLineFromSpace in maroto's
// internal/providers/gofpdf/text.go. It is duplicated rather than called
// because it is unexported there, and it is transcribed rather than
// approximated because the two must agree: if this returns 2 where maroto
// renders 3, the third line is drawn outside its row and lands on top of the
// next one.
//
// The deck renderer uses the same function against PowerPoint, which wraps
// greedily at spaces too. There it is an estimate rather than a transcription —
// no OOXML consumer will tell us where it broke a line — which is why the deck
// declares normAutofit on top of it.
func Wrap(s string, family string, style Style, sizePt, widthMM float64) []string {
	p, merr := measurer()
	if merr != nil || p == nil || widthMM <= 0 {
		return []string{s}
	}
	mu.Lock()
	defer mu.Unlock()
	p.SetFont(family, string(style), sizePt)

	var (
		lines   []string
		current float64
	)
	for word := range strings.SplitSeq(s, " ") {
		if word == "" {
			continue
		}
		piece, separator := word, ""
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			piece, separator = " "+word, " "
		}
		width := p.GetStringWidth(piece)
		if current+width <= widthMM {
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

// Truncate shortens s until it wraps into at most maxLines, appending an
// ellipsis when it had to cut.
//
// PowerPoint's renderer silently clips overflow and T-R4 treats that as an
// acceptance failure; the same standard applies to the PDF. A visible "…" tells
// the reader the cell was longer than the column. A cell that simply stops
// mid-word tells them nothing, and a cell drawn over the row beneath it tells
// them something false.
func Truncate(s string, family string, style Style, sizePt, widthMM float64, maxLines int) string {
	if maxLines <= 0 {
		return s
	}
	lines := Wrap(s, family, style, sizePt, widthMM)
	if len(lines) <= maxLines {
		return s
	}
	kept := strings.Join(lines[:maxLines], " ")
	runes := []rune(kept)
	for len(runes) > 1 {
		candidate := strings.TrimRight(string(runes), " ") + "…"
		if len(Wrap(candidate, family, style, sizePt, widthMM)) <= maxLines {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

// Fit is Truncate plus the case it cannot handle: a token with no spaces in it.
//
// Line breaking happens at spaces and nowhere else, so "SO-2026-4100" in a 20mm
// column is one line 26mm wide, drawn straight over the column to its right.
// Line-count truncation never fires because the text is already one line. The
// 200-row export fixture is where this shows up, and it shows up as an order
// number printed across a customer name.
//
// So each wrapped line is measured, and any line still wider than the column is
// cut by characters until it fits with an ellipsis. Breaking mid-token is not
// pretty; a cell bleeding into its neighbour is not readable.
func Fit(s string, family string, style Style, sizePt, widthMM float64, maxLines int) string {
	s = Truncate(s, family, style, sizePt, widthMM, maxLines)
	lines := Wrap(s, family, style, sizePt, widthMM)

	changed := false
	for i, ln := range lines {
		if Width(ln, family, style, sizePt) <= widthMM {
			continue
		}
		lines[i] = clipRunes(ln, family, style, sizePt, widthMM)
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(lines, " ")
}

// clipRunes cuts a single line to width, ending in an ellipsis.
func clipRunes(s string, family string, style Style, sizePt, widthMM float64) string {
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := strings.TrimRight(string(runes), " ") + "…"
		if Width(candidate, family, style, sizePt) <= widthMM {
			return candidate
		}
	}
	return "…"
}
