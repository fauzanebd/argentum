// Package numparse reads a number written the way a person writes it, in
// either of the two separator conventions this product serves.
//
// **It exists because there were two of these and T-P4 needed a third.**
// `guardrails.parseLoose` read figures out of a reply, `guardrails.parseFigure`
// read the two halves of a restatement, and the typing layer needs the same
// question answered about a cell in a PDF. Three implementations of "is `1.234`
// one thousand two hundred and thirty-four?" is three chances to answer it
// differently, and the failure that would produce — a column a thousand times
// off — is the exact defect this roadmap's arithmetic check exists to catch.
// The promotion is the one `metric.ValidateTemplate` made into `sqlguard`, for
// the same reason and with the same rule: after this there is one.
//
// The ambiguity is genuine and cannot be resolved by asking the locale.
// Indonesian groups with "." and points with ","; English is the reverse; a
// reply in Indonesian carrying a USD figure mixes both in one sentence, and a
// PDF exported by an ERP configured in one locale is read by a tenant working
// in the other. So every decision here is **structural** — made from the
// token's own shape — and where the shape is genuinely ambiguous the caller
// that knows more decides: [ParseWithDecimal] is what T-P4's column-majority
// pass uses, because a column of cells carries evidence one cell does not.
package numparse

import (
	"strconv"
	"strings"
)

// Parse reads a number whose separator convention is unknown.
//
// The rules, in the order they are tried:
//
//   - Both separators present: the rightmost is the decimal point and the other
//     groups. "12,462,599.03" and "12.462.599,03" are the same number.
//   - The same separator more than once: it groups. "3.863.405.700".
//   - Exactly one separator with exactly three digits after it: a group only
//     when what precedes it is itself a group of one to three digits.
//     "12.500" is twelve thousand five hundred; "1234.000" is a driver
//     rendering DECIMAL(…,3) and is 1234.
//   - Anything else: a decimal separator. This is what makes "21,23" read as
//     21.23 rather than 2123 — the defect observed on 2026-08-14, on the
//     product's primary language, in an instrument whose whole value is that
//     its output is worth reading.
func Parse(raw string) (float64, bool) {
	raw = strings.Trim(raw, ".,")
	if raw == "" {
		return 0, false
	}
	dots := strings.Count(raw, ".")
	commas := strings.Count(raw, ",")

	switch {
	case dots > 0 && commas > 0:
		if strings.LastIndex(raw, ".") > strings.LastIndex(raw, ",") {
			raw = strings.ReplaceAll(raw, ",", "")
		} else {
			raw = strings.ReplaceAll(raw, ".", "")
			raw = strings.ReplaceAll(raw, ",", ".")
		}
	case dots > 1:
		raw = strings.ReplaceAll(raw, ".", "")
	case commas > 1:
		raw = strings.ReplaceAll(raw, ",", "")
	case dots == 1 || commas == 1:
		sep := "."
		if commas == 1 {
			sep = ","
		}
		i := strings.Index(raw, sep)
		before, after := raw[:i], raw[i+1:]
		if len(after) == 3 && len(before) >= 1 && len(before) <= 3 {
			raw = before + after
		} else {
			raw = before + "." + after
		}
	}

	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ParseWithDecimal reads a number when the caller already knows which character
// is the decimal separator and which one groups.
//
// This is the entry point for a column of cells (T-P4). One cell of a PDF table
// cannot say whether "1.234" is a group or a decimal; the column can, by
// majority over all of its cells, and deciding per cell is how a column ends up
// with three values a thousand times larger than the rest of it. `dec` is '.'
// or ','; anything else falls back to [Parse], because a caller that could not
// decide should not be believed about the decision.
func ParseWithDecimal(raw string, dec byte) (float64, bool) {
	if dec != '.' && dec != ',' {
		return Parse(raw)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	group := byte('.')
	if dec == '.' {
		group = ','
	}
	raw = strings.ReplaceAll(raw, string(group), "")
	if dec == ',' {
		raw = strings.ReplaceAll(raw, ",", ".")
	}
	// A trailing separator survives the replacement above — "1.234." — and
	// ParseFloat refuses it. Trim rather than refuse: the document meant the
	// number, and a stray full stop at the end of a cell is punctuation.
	raw = strings.Trim(raw, ".")
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// magnitudes maps every magnitude word this product's system prompt teaches the
// agent to write — English and Indonesian — to its multiplier.
//
// The Indonesian spellings are not a courtesy. The primary language of this
// deployment's traffic writes "3,86 Miliar", and an instrument that reads only
// the English half is an instrument that is blind on most of the traffic — the
// T-Q3 lesson, learned by shipping three `must_not_call` assertions in English
// and watching the violation land in Indonesian.
var magnitudes = map[string]float64{
	"thousand": 1e3, "ribu": 1e3,
	"million": 1e6, "juta": 1e6,
	"billion": 1e9, "miliar": 1e9, "milyar": 1e9,
	"trillion": 1e12, "triliun": 1e12,
}

// canonical is the spelling a correction writes back, per language: English
// first, Indonesian second. A reply that said "miliar" gets "miliar" back
// rather than "billion".
var canonical = map[float64][2]string{
	1e3:  {"thousand", "ribu"},
	1e6:  {"million", "juta"},
	1e9:  {"billion", "miliar"},
	1e12: {"trillion", "triliun"},
}

// Magnitude returns the multiplier a magnitude word names, case-insensitively.
func Magnitude(word string) (float64, bool) {
	m, ok := magnitudes[strings.ToLower(strings.TrimSpace(word))]
	return m, ok
}

// CanonicalMagnitude returns the English and Indonesian spellings of a
// multiplier.
func CanonicalMagnitude(multiplier float64) (english, indonesian string, ok bool) {
	pair, found := canonical[multiplier]
	if !found {
		return "", "", false
	}
	return pair[0], pair[1], true
}

// Multipliers lists the magnitude multipliers in ascending order.
//
// Ascending and fixed rather than ranged over the map, because a caller asking
// "which multiplier makes this restatement agree?" can have two answers when a
// figure is small, and a map's iteration order would hand it a different one on
// different runs. A deterministic instrument that is occasionally imprecise is
// worth more than a random one that is occasionally exact.
func Multipliers() []float64 { return []float64{1e3, 1e6, 1e9, 1e12} }

// MagnitudeWords lists every recognised magnitude word. Sorted by descending
// length so a scanner matching them in order finds "milyar" before "mil…"
// would have been a prefix of anything shorter — and so a caller building an
// alternation gets the longest match first, which is the only order that is
// correct for one.
func MagnitudeWords() []string {
	words := make([]string, 0, len(magnitudes))
	for w := range magnitudes {
		words = append(words, w)
	}
	// Insertion sort: the list is eight items and stays eight items, and a
	// deterministic order matters more here than the sort's shape — map
	// iteration is random, and a regexp built from a randomly ordered
	// alternation is a test that passes four times in five.
	for i := 1; i < len(words); i++ {
		for j := i; j > 0 && lessForAlternation(words[j], words[j-1]); j-- {
			words[j], words[j-1] = words[j-1], words[j]
		}
	}
	return words
}

func lessForAlternation(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a < b
}
