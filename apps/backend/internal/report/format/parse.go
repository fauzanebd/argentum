// Package format is the single place Argentum turns numbers into text and
// text back into numbers.
//
// It exists because two subsystems need to agree on exactly the same
// conventions and must not each grow their own opinion:
//
//   - The eval harness (T-01) reads a number out of an agent reply so it can
//     compare it to a known-correct value. It needs the parsing direction.
//   - The report renderers (T-R2 onwards) write numbers into PDF cells, chart
//     axes and slide tiles. They need the formatting direction.
//
// If an axis label says "Rp 3,86 Miliar" and the eval comparator reads that
// as 3.86, the product is broken in a way no test would catch. One package,
// both directions, one set of rules.
//
// Locale conventions handled here:
//
//	en: 1,234,567.89   $1.2K    3.86 billion
//	id: 1.234.567,89   Rp 1,2 Juta   Rp 3,86 Miliar
package format

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Number is one numeric value recovered from free text, with enough
// provenance for a caller to decide whether it is the value it was looking
// for.
type Number struct {
	// Value is the parsed magnitude, with any suffix already applied:
	// "3,86 Miliar" yields 3.86e9, not 3.86.
	Value float64

	// Raw is the exact substring the value was parsed from, suffix included.
	Raw string

	// Magnitude is the multiplier a suffix contributed (1 when there was
	// none). A comparator can use this to be more forgiving about rounding:
	// "Rp 3,86 Miliar" is a correct rendering of 3_863_405_700 even though
	// the values differ by millions.
	Magnitude float64

	// Currency is the symbol or code seen immediately before the digits
	// ("Rp", "$", "IDR", …), empty when the number was bare.
	Currency string

	// IsPercent is true when the number was written with a trailing %.
	IsPercent bool

	// magnitudeToken is the suffix text that produced Magnitude. Kept only
	// so parsing can prefer the longest match ("miliar" over "m").
	magnitudeToken string
}

// magnitudeSuffixes maps a lower-cased suffix token to its multiplier.
//
// Single letters are read as English (K/M/B/T). Indonesian magnitudes are
// always words (juta / miliar / triliun) and never collide with those
// letters — which matters, because "M" means million to an English reader
// and is sometimes typed for "miliar" by an Indonesian one. The English
// reading wins for bare letters; anything else must be spelled out. That
// choice is arbitrary but it has to be written down somewhere, so it is
// written down here.
var magnitudeSuffixes = map[string]float64{
	// Indonesian
	"ribu":    1e3,
	"rb":      1e3,
	"juta":    1e6,
	"jt":      1e6,
	"miliar":  1e9,
	"milyar":  1e9,
	"miliyar": 1e9,
	"triliun": 1e12,
	"trilyun": 1e12,

	// English
	"k":        1e3,
	"thousand": 1e3,
	"m":        1e6,
	"mn":       1e6,
	"million":  1e6,
	"millions": 1e6,
	"b":        1e9,
	"bn":       1e9,
	"billion":  1e9,
	"billions": 1e9,
	"t":        1e12,
	"tn":       1e12,
	"trillion": 1e12,
}

// currencyPrefixes are stripped from the front of a number token. Longest
// first so "IDR" is matched before "I" would be considered.
var currencyPrefixes = []string{"IDR", "USD", "EUR", "SGD", "MYR", "Rp.", "Rp", "RM", "S$", "US$", "$", "€", "£", "¥"}

// numberPattern finds candidate numeric runs in free text: an optional
// currency prefix, a digit run that may carry grouping and decimal
// separators, and an optional magnitude suffix or percent sign.
//
// Deliberately permissive — resolving what the separators mean is
// resolveSeparators' job, not the regexp's.
// The word-suffix branch carries its own \b so "3 minutes" does not read as
// 3 million; the % branch must not, because there is no word boundary
// between '%' and the space after it.
var numberPattern = regexp.MustCompile(
	`(?i)-?\s?` + // a minus may sit in front of the symbol: -$1,234.00
		`(IDR|USD|EUR|SGD|MYR|Rp\.?|RM|US\$|S\$|\$|€|£|¥)?\s?` + // currency
		`(-?\d[\d.,]*\d|-?\d)` + // digits with separators
		`(?:\s?(%|(?:ribu|rb|juta|jt|miliar|milyar|miliyar|triliun|trilyun|thousand|millions|million|billions|billion|trillion|mn|bn|tn|k|m|b|t)\b))?`,
)

// Parse reads a single number from s, keeping its provenance. The whole
// string must be one numeric expression — leading and trailing whitespace
// is fine, other text is not. Use ExtractNumbers to pull numbers out of
// prose.
func Parse(s string) (Number, bool) {
	return parseOne(strings.TrimSpace(s), true)
}

// ParseNumber is Parse when the caller only wants the value.
func ParseNumber(s string) (float64, bool) {
	n, ok := Parse(s)
	if !ok {
		return 0, false
	}
	return n.Value, true
}

// ExtractNumbers returns every number found in text, in order of
// appearance. Prose, markdown tables and bullet lists are all fine input.
//
// This is what the eval harness runs over an agent reply: it does not know
// where in the sentence the answer sits, only that one of the numbers in
// there should match.
func ExtractNumbers(text string) []Number {
	matches := numberPattern.FindAllStringSubmatchIndex(text, -1)
	out := make([]Number, 0, len(matches))
	for _, m := range matches {
		raw := strings.TrimSpace(text[m[0]:m[1]])
		n, ok := parseOne(raw, false)
		if !ok {
			continue
		}
		out = append(out, n)
	}
	return out
}

// parseOne turns one already-isolated token into a Number. strict rejects
// anything with leftover characters; non-strict is used when the caller has
// already sliced the token out of a larger string.
func parseOne(s string, strict bool) (Number, bool) {
	if s == "" {
		return Number{}, false
	}

	var n Number
	rest := strings.TrimSpace(s)

	// A minus in front of the currency symbol. "-$1,234.00" and "-Rp 1.234"
	// are what every renderer writes, including this package's own — the
	// alternative, "$-1,234.00", is not written by anyone. The sign is taken
	// off here and put back on the value at the end, so the separator logic
	// below never has to think about it.
	negative := strings.HasPrefix(rest, "-")
	if negative {
		rest = strings.TrimSpace(rest[1:])
	}

	// Currency prefix.
	for _, c := range currencyPrefixes {
		if len(rest) >= len(c) && strings.EqualFold(rest[:len(c)], c) {
			n.Currency = strings.TrimSuffix(c, ".")
			rest = strings.TrimSpace(rest[len(c):])
			break
		}
	}

	// Trailing percent or magnitude suffix.
	if strings.HasSuffix(rest, "%") {
		n.IsPercent = true
		rest = strings.TrimSpace(strings.TrimSuffix(rest, "%"))
	} else {
		lower := strings.ToLower(rest)
		for suffix, mult := range magnitudeSuffixes {
			if !strings.HasSuffix(lower, suffix) {
				continue
			}
			head := strings.TrimSpace(rest[:len(rest)-len(suffix)])
			// The remainder must end in a digit, or we have matched the tail
			// of a word ("current" ending in "t") rather than a suffix.
			if head == "" || !isDigit(head[len(head)-1]) {
				continue
			}
			// Prefer the longest suffix: "miliar" must not lose to "m".
			if n.Magnitude != 0 && len(suffix) <= len(n.magnitudeToken) {
				continue
			}
			n.Magnitude = mult
			n.magnitudeToken = suffix
		}
		if n.Magnitude != 0 {
			rest = strings.TrimSpace(rest[:len(rest)-len(n.magnitudeToken)])
		}
	}
	if n.Magnitude == 0 {
		n.Magnitude = 1
	}

	digits := resolveSeparators(strings.ReplaceAll(rest, " ", ""))
	if digits == "" {
		return Number{}, false
	}
	v, err := strconv.ParseFloat(digits, 64)
	if err != nil {
		return Number{}, false
	}
	if strict && !isNumericToken(rest) {
		return Number{}, false
	}

	n.Value = v * n.Magnitude
	if negative {
		n.Value = -n.Value
	}
	n.Raw = strings.TrimSpace(s)
	return n, true
}

// resolveSeparators converts a grouped number into something ParseFloat
// accepts, deciding which of '.' and ',' is the decimal point.
//
// The rules, in order:
//
//  1. Both separators present → whichever appears last is the decimal
//     point. "1.234,56" is id, "1,234.56" is en. This case is unambiguous.
//  2. One separator, appearing more than once → it is a group separator.
//     "1.234.567" cannot be a decimal point twice.
//  3. One separator, once, with exactly 3 digits after it → group
//     separator. "1,234" reads as 1234, which is the common case in both
//     locales. Cost: a genuine "1,234" meaning 1.234 is misread — accepted,
//     because three-decimal money does not occur in either locale.
//  4. Otherwise → decimal point. "1,5" and "3.86" both parse as written.
func resolveSeparators(s string) string {
	if s == "" {
		return ""
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	lastDot := strings.LastIndex(s, ".")
	lastComma := strings.LastIndex(s, ",")

	var decimalSep, groupSep string
	switch {
	case lastDot >= 0 && lastComma >= 0:
		if lastDot > lastComma {
			decimalSep, groupSep = ".", ","
		} else {
			decimalSep, groupSep = ",", "."
		}
	case lastDot >= 0:
		decimalSep, groupSep = classifySingle(s, '.')
	case lastComma >= 0:
		decimalSep, groupSep = classifySingle(s, ',')
	}

	if groupSep != "" {
		s = strings.ReplaceAll(s, groupSep, "")
	}
	if decimalSep != "" && decimalSep != "." {
		s = strings.ReplaceAll(s, decimalSep, ".")
	}
	if neg {
		s = "-" + s
	}
	// Reject anything that is not now a plain number: stray separators mean
	// the token was never a number to begin with.
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isDigit(c) || c == '.' || (i == 0 && c == '-') {
			continue
		}
		return ""
	}
	return s
}

// classifySingle decides whether a lone separator is a decimal point or a
// group separator. Returns (decimalSep, groupSep), one of which is empty.
func classifySingle(s string, sep byte) (string, string) {
	first := strings.IndexByte(s, sep)
	last := strings.LastIndexByte(s, sep)
	if first != last {
		return "", string(sep) // rule 2: repeated → grouping
	}
	tail := s[last+1:]
	if len(tail) == 3 {
		return "", string(sep) // rule 3: exactly three trailing digits
	}
	return string(sep), "" // rule 4
}

func isNumericToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isDigit(c) || c == '.' || c == ',' || c == ' ' || (i == 0 && c == '-') {
			continue
		}
		return false
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Matches reports whether got is within tolerance of want.
//
// tolerance is relative (0.01 = 1%). A magnitude-suffixed answer gets extra
// slack: "Rp 3,86 Miliar" is a correct answer to a question whose true value
// is 3_863_405_700 even though it is 5.4 million off, because the agent was
// asked to be readable. The slack is half a unit of the last significant
// digit the suffix form can express.
func Matches(got Number, want, tolerance float64) bool {
	if tolerance <= 0 {
		tolerance = 0.001
	}
	if want == 0 {
		return math.Abs(got.Value) < 1e-9
	}
	rel := math.Abs(got.Value-want) / math.Abs(want)
	if rel <= tolerance {
		return true
	}
	if got.Magnitude > 1 {
		// Two decimals of a suffixed unit: 3.86 Miliar can only be as
		// precise as ±0.005e9.
		if math.Abs(got.Value-want) <= 0.005*got.Magnitude {
			return true
		}
	}
	return false
}
