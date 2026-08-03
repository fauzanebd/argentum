package guardrails

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The scale check: a figure and its own restatement, in one reply, must agree.
//
// `T-16` proves a number was *queried*. It does not prove it was *printed* at
// the right magnitude, and the gap is not theoretical — it has been observed
// twice, once in chat (`T-B1`'s gate, 2026-07-31) and once in a watcher
// briefing a customer receives unprompted (the 2026-08-02 gate):
//
//	"IDR 3,863,405,700 (approximately $3.86 million)"
//
// The figure is right, tool-derived, and passes the fabrication check. The
// parenthetical is wrong by a factor of a thousand, and it is the half a reader
// remembers. Nothing owned this until now.
//
// What this check does NOT do is compare a reply's figures against the numbers a
// tool returned. That needs the values plumbed through TurnEvidence, and it
// carries the false-positive risk fabrication.go's comment names: a legitimate
// sum, rate or rounding is not in any tool result. This is the half that needs
// no evidence at all, because the reply contradicts *itself* — and a reply that
// contradicts itself is never correct, whatever the tools returned.

// magnitudeWords maps every magnitude word the system prompt teaches the agent
// to use — English and Indonesian — to its multiplier.
var magnitudeWords = map[string]float64{
	"thousand": 1e3, "ribu": 1e3,
	"million": 1e6, "juta": 1e6,
	"billion": 1e9, "miliar": 1e9, "milyar": 1e9,
	"trillion": 1e12, "triliun": 1e12,
}

// canonicalWord is the spelling a correction writes back, per language. A reply
// that said "miliar" gets "miliar" rather than "billion".
var canonicalWord = map[float64][2]string{
	1e3:  {"thousand", "ribu"},
	1e6:  {"million", "juta"},
	1e9:  {"billion", "miliar"},
	1e12: {"trillion", "triliun"},
}

// restatement matches "<number> (approximately <number> <magnitude word>)" and
// the variants that appear in practice: "≈", "~", "about", "roughly", "sekitar",
// "kira-kira", with or without a currency symbol and with or without brackets.
//
// The anchor is the approximation marker, not the bracket: a restatement
// introduced by "about" outside brackets is the same claim and the same defect.
var restatement = regexp.MustCompile(
	`(?i)(\d[\d.,]*)\s*` + // the figure as stated
		`(?:\)|\]|,|—|-|\s)*\s*` +
		`[\(\[]?\s*` +
		`(?:approximately|approx\.?|about|roughly|around|circa|≈|~|sekitar|kira-kira|kurang lebih)\s*` +
		`(?:rp\.?|idr|usd|sgd|myr|eur|gbp|\$|€|£|¥)?\s*` +
		`(\d[\d.,]*)\s*` + // the restated figure
		`(thousand|million|billion|trillion|ribu|juta|miliar|milyar|triliun)\b`, // and its unit
)

// ScaleCorrection is one restatement whose unit did not match its own figure.
type ScaleCorrection struct {
	// Stated is the figure as first written, Restated the approximation beside
	// it, and Was / Now the magnitude word before and after.
	Stated   float64
	Restated float64
	Was      string
	Now      string
}

func (c ScaleCorrection) String() string {
	return fmt.Sprintf("%.0f restated as %g %s, corrected to %g %s", c.Stated, c.Restated, c.Was, c.Restated, c.Now)
}

// CheckScale corrects a magnitude word that disagrees with the figure it
// restates, and reports what it changed.
//
// It only ever rewrites the *unit*, and only when the restated digits are right
// under some other unit — "3,863,405,700 … 3.86 million" becomes "3.86 billion",
// because 3.86 × 10⁹ is what the figure says and 3.86 is what the model already
// typed. When no unit makes the restatement agree, nothing is rewritten: the two
// numbers genuinely disagree, which is a different failure and not one arithmetic
// can repair. The caller logs those.
//
// Rewriting a word the model wrote is a real intervention, so the bar for it is
// that the correction is *derivable from the reply itself* — same digits, same
// sentence, one unit apart. No tool result, no model call, no judgement.
func CheckScale(reply string) (string, []ScaleCorrection) {
	if strings.TrimSpace(reply) == "" {
		return reply, nil
	}
	var fixes []ScaleCorrection

	out := restatement.ReplaceAllStringFunc(reply, func(match string) string {
		groups := restatement.FindStringSubmatch(match)
		if len(groups) != 4 {
			return match
		}
		stated, ok := parseFigure(groups[1])
		if !ok || stated == 0 {
			return match
		}
		restated, ok := parseFigure(groups[2])
		if !ok || restated == 0 {
			return match
		}
		word := strings.ToLower(groups[3])
		mult, ok := magnitudeWords[word]
		if !ok {
			return match
		}
		if agrees(stated, restated*mult) {
			return match
		}
		// The restatement is wrong. Is it wrong only in its unit?
		want, found := 0.0, false
		for m := range canonicalWord {
			if agrees(stated, restated*m) {
				want, found = m, true
				break
			}
		}
		if !found {
			return match
		}
		replacement := canonicalWord[want][0]
		if isIndonesianWord(word) {
			replacement = canonicalWord[want][1]
		}
		fixes = append(fixes, ScaleCorrection{
			Stated: stated, Restated: restated, Was: word, Now: replacement,
		})
		// Replace the last occurrence of the unit inside the matched span, so a
		// figure that happens to contain the word elsewhere is untouched.
		i := strings.LastIndex(strings.ToLower(match), word)
		return match[:i] + matchCase(match[i:i+len(word)], replacement) + match[i+len(word):]
	})

	return out, fixes
}

// agrees allows the rounding a restatement is for. "3.86 billion" for
// 3,863,405,700 is 0.09% off and correct; a factor of 1000 is not rounding.
func agrees(stated, restated float64) bool {
	if stated == 0 {
		return restated == 0
	}
	diff := stated - restated
	if diff < 0 {
		diff = -diff
	}
	return diff/abs(stated) <= 0.05
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func isIndonesianWord(w string) bool {
	switch w {
	case "ribu", "juta", "miliar", "milyar", "triliun":
		return true
	}
	return false
}

// matchCase carries the original word's capitalisation onto its replacement, so
// "3,86 Miliar" does not become "3,86 miliar" in a sentence that capitalised it.
func matchCase(original, replacement string) string {
	if original == "" || replacement == "" {
		return replacement
	}
	if original == strings.ToUpper(original) {
		return strings.ToUpper(replacement)
	}
	if original[0] >= 'A' && original[0] <= 'Z' {
		return strings.ToUpper(replacement[:1]) + replacement[1:]
	}
	return replacement
}

// parseFigure reads a number written in either locale's conventions.
//
// The ambiguity is real and unavoidable: "3.863" is three thousand eight
// hundred and sixty-three in Indonesian and three point eight six three in
// English. It is resolved structurally rather than by locale, because a reply
// mixes both — an Indonesian answer with a USD figure in it is ordinary here.
// A separator followed by exactly three digits, appearing more than once or
// alongside the other separator, is a group separator; anything else is a
// decimal point.
func parseFigure(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	dots := strings.Count(s, ".")
	commas := strings.Count(s, ",")

	switch {
	case dots > 0 && commas > 0:
		// Both present: the rightmost is the decimal separator.
		if strings.LastIndex(s, ".") > strings.LastIndex(s, ",") {
			s = strings.ReplaceAll(s, ",", "")
		} else {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		}
	case dots > 1:
		s = strings.ReplaceAll(s, ".", "")
	case commas > 1:
		s = strings.ReplaceAll(s, ",", "")
	case dots == 1:
		if isGroup(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
		}
	case commas == 1:
		if isGroup(s, ",") {
			s = strings.ReplaceAll(s, ",", "")
		} else {
			s = strings.Replace(s, ",", ".", 1)
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// isGroup reports whether a single separator is a thousands separator: exactly
// three digits follow it and something precedes it.
func isGroup(s, sep string) bool {
	i := strings.Index(s, sep)
	return i > 0 && len(s)-i-1 == 3
}
