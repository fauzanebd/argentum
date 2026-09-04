package videoplan

import (
	"strings"
	"unicode/utf8"

	"github.com/fauzanebd/argentum/internal/report/labels"
)

// MaxAltChars is Instagram's cap on a child's alt text.
const MaxAltChars = 1000

// altText describes a still in words, from the strings the scene already
// carries (T-G3).
//
// Nothing here formats, translates or decides: every value was formatted by
// the same format package the slide's own text went through, and every label
// is the locale's, so an Indonesian carousel gets Indonesian alt text without
// anyone asking for it. The shape is deliberately plain — the title, then what
// the slide shows, joined with separators a screen reader pauses on — because
// alt text is read aloud, not laid out, and prose that tries to be a caption
// ends up describing the design rather than the figure.
//
// A table's rows are not read out: twelve rows of six cells is a paragraph of
// numbers nobody can hold, and the header with the caption says what the table
// is about. A chart is its title and caption for the same reason — the caption
// is where the chart already says what it shows.
func altText(s Scene, words labels.Set) string {
	var parts []string
	add := func(strs ...string) {
		joined := strings.TrimSpace(strings.Join(strs, " "))
		if joined != "" {
			parts = append(parts, joined)
		}
	}

	add(s.Title...)
	add(s.Subtitle...)
	if s.Period != "" {
		add(s.Period)
	}
	add(s.Lines...)

	for _, k := range s.KPIs {
		card := k.Label + ": " + k.Value
		if k.Delta != "" {
			card += " (" + k.Delta + ")"
		}
		add(card)
	}
	for _, f := range s.Facts {
		add(f.Label + ": " + strings.Join(f.Value, " "))
	}
	if s.Table != nil {
		add(strings.Join(nonEmpty(s.Table.Header), ", "))
		if len(s.Table.Total) > 0 {
			add(strings.Join(nonEmpty(s.Table.Total), " "))
		}
	}
	add(s.Caption...)
	if s.Continued && words.Continued != "" {
		add(words.Continued)
	}

	return capRunes(strings.Join(parts, ". "), MaxAltChars)
}

func nonEmpty(strs []string) []string {
	out := make([]string, 0, len(strs))
	for _, s := range strs {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// capRunes truncates s to at most n runes, at a word boundary where one is
// near, with an ellipsis that says it was cut.
func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)[:n-1]
	cut := len(runes)
	for i := len(runes) - 1; i > n/2; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(runes[:cut]), " .,;:") + "…"
}
