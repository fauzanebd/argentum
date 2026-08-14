package guardrails

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Grounding answers a question CheckFabrication does not ask (T-Q9): not
// "did any tool return anything?", but "is THIS number one of the numbers a
// tool returned?"
//
// **The gap between the two.** CheckFabrication is a gate on evidence
// existing. Its own predicate is `ev.grounded()` — a data tool ran and brought
// back rows — so a turn that queried, got 3,863,405,700, and then wrote
// "roughly 4.1 billion" passes it completely. Every gate this product has
// treats that as a correct answer: the query ran, rows came back, a figure was
// stated, the magnitudes agree. This is the wrong-but-nonempty class the eval
// set's `wrong_grain` category exists for, seen from the answer's side rather
// than the query's.
//
// **Why it warns rather than blocks.** An analyst's reply legitimately
// contains numbers no query returned: a difference between two results, a
// percentage, a per-unit figure, a year in a date, a row count read off the
// list. Blocking those would be the guardrail-overreach cycle this repo has
// already lived through once — six of the last twenty pre-sprint commits
// narrowed a guardrail regex after it blocked something legitimate. So this
// reports, and what it reports is a log line and a metric, not a replacement.
//
// That makes it an instrument rather than a gate, which is what the roadmap
// asks of it: today the wrong-but-nonempty rate is not merely unenforced, it
// is *unmeasured*, and nothing can be tightened before it is counted.

// ungroundedTolerance is how close a stated figure has to be to a returned one
// to count as the same number.
//
// One percent, matching the eval harness's default numeric tolerance and for
// the same reason: the agent is instructed to write readable numbers, so
// "Rp 3,86 Miliar" is the correct rendering of 3,863,405,700 and must not read
// as a different figure.
const ungroundedTolerance = 0.01

// GroundingReport is what one reply's figures were checked against.
type GroundingReport struct {
	// Stated is every figure found in the reply's prose.
	Stated []float64
	// Ungrounded is the subset matching nothing a tool returned and nothing
	// simply derivable from it.
	Ungrounded []float64
	// Checked is false when the turn returned no numbers at all, which makes
	// the whole comparison meaningless — every stated figure would be
	// "ungrounded" and the report would be noise. CheckFabrication is the gate
	// for that case and it already covers it.
	Checked bool
}

// Clean reports whether every stated figure was accounted for.
func (r GroundingReport) Clean() bool { return !r.Checked || len(r.Ungrounded) == 0 }

// figureInProse finds the numbers a reply asserts. Deliberately narrower than
// format.ExtractNumbers: it runs over the same stripped prose StatesFigure
// uses, so markdown links and code blocks — full of digits that are not claims
// about the business — are out of scope.
var figureInProse = regexp.MustCompile(`\d[\d.,]*`)

// CheckGrounding compares the figures in a reply against the numbers the
// turn's tools actually returned.
//
// returned is every numeric value seen in this turn's tool results, in any
// order. An empty slice means the comparison cannot be made and Checked comes
// back false.
func CheckGrounding(reply string, returned []float64) GroundingReport {
	var rep GroundingReport
	if strings.TrimSpace(reply) == "" || len(returned) == 0 {
		return rep
	}
	rep.Checked = true
	rep.Stated = extractProseFigures(reply)

	for _, want := range rep.Stated {
		if !grounded(want, returned) {
			rep.Ungrounded = append(rep.Ungrounded, want)
		}
	}
	return rep
}

// extractProseFigures pulls the numbers out of a reply's prose, skipping the
// ones that are never claims about the business.
func extractProseFigures(reply string) []float64 {
	prose := stripNonProse(reply)
	seen := map[float64]bool{}
	var out []float64
	for _, raw := range figureInProse.FindAllString(prose, -1) {
		// figureInProse ends at `[\d.,]*`, so a figure that closes a sentence
		// arrives with the full stop attached — "…31 December 2024." matches as
		// `2024.`. parseLoose trims that before parsing; isBareYear below reads
		// the token's punctuation to tell a year from a quantity, so it has to
		// be handed the same trimmed token or every sentence-final year looks
		// like a decimal. Trimming once, here, keeps the two in agreement.
		tok := strings.Trim(raw, ".,")
		v, ok := parseLoose(tok)
		if !ok {
			continue
		}
		// Small integers are list indices, row counts, quarters, months and
		// "the top 5" — almost never a figure anyone would fabricate, and the
		// overwhelming majority of false positives. The failure this exists to
		// catch is a large number quoted wrongly.
		if v < 1000 {
			continue
		}
		// A bare four-digit number in the calendar range is a year. This is the
		// single commonest false positive above the cutoff: every reply about a
		// month names one, and no query returns it as a value.
		//
		// The cost is that a genuine figure of exactly 2024 goes unchecked —
		// the same trade the cutoff above makes, and the right one, because
		// this is an instrument whose usefulness depends on its output being
		// worth reading.
		if isBareYear(tok, v) {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// isBareYear reports whether a token is a four-digit calendar year written
// without separators. "2024" is; "2,024" and "2024.50" are not — a writer who
// grouped or fractioned it meant a quantity.
func isBareYear(raw string, v float64) bool {
	if strings.ContainsAny(raw, ".,") || len(raw) != 4 {
		return false
	}
	return v >= 1900 && v <= 2100 && v == math.Trunc(v)
}

// grounded reports whether a stated figure is one a tool returned, or one
// simply derived from what they returned.
//
// The derivations recognised are the ones an analyst writes without thinking:
// a magnitude rendering (3.86 for 3,863,405,700), a sum of two returned
// values, and a difference between two. Anything more elaborate — a weighted
// average, a compound rate — is left unrecognised, which is why this reports
// rather than blocks.
func grounded(want float64, returned []float64) bool {
	for _, got := range returned {
		if closeEnough(want, got) || sameMagnitudeRendering(want, got) {
			return true
		}
	}
	// A sum or a difference of two returned values. Quadratic, and bounded by
	// the cap the caller applies to `returned` — a turn's tool results are tens
	// of numbers, not thousands.
	for i := range returned {
		for j := range returned {
			if i == j {
				continue
			}
			if closeEnough(want, returned[i]+returned[j]) ||
				closeEnough(want, returned[i]-returned[j]) {
				return true
			}
		}
	}
	return false
}

func closeEnough(a, b float64) bool {
	if a == b {
		return true
	}
	if b == 0 {
		return math.Abs(a) < 1e-9
	}
	return math.Abs(a-b)/math.Abs(b) <= ungroundedTolerance
}

// sameMagnitudeRendering matches a figure written in magnitude units against
// the raw value: "Rp 3,86 Miliar" against 3,863,405,700.
//
// The system prompt requires exactly this rendering for Indonesian replies —
// Juta, Miliar, Triliun — so without it every correctly formatted Indonesian
// answer would report as ungrounded, which would make the instrument useless
// on half the traffic.
func sameMagnitudeRendering(stated, actual float64) bool {
	for _, scale := range []float64{1e3, 1e6, 1e9, 1e12} {
		if closeEnough(stated*scale, actual) {
			return true
		}
	}
	return false
}

// parseLoose reads a number written with either separator convention.
//
// Indonesian uses "." for thousands and "," for decimals; English is the
// reverse; and the reply is required to follow the user's language. The
// convention is decided from the token's own shape rather than from the
// locale, because the locale is not available here and the reply may mix
// languages anyway.
//
// This used to try English first and return the first reading that parsed,
// which is not the same thing as the comment above it claimed ("the reading
// that yields a plausible number wins"). Stripping the commas out of an
// Indonesian decimal always parses: "Rp 21,23 Miliar" came back as **2123**,
// a four-digit integer a hundred times the real value that no tool ever
// returned — so every Indonesian magnitude sentence produced a spurious
// ungrounded figure. Observed on two different models on 2026-08-14, on the
// product's primary language, in the instrument whose whole value is that its
// output is worth reading.
func parseLoose(raw string) (float64, bool) {
	raw = strings.Trim(raw, ".,")
	if raw == "" {
		return 0, false
	}
	dots := strings.Count(raw, ".")
	commas := strings.Count(raw, ",")

	switch {
	case dots > 0 && commas > 0:
		// Both present: the rightmost is the decimal point and the other groups.
		// "12,462,599.03" and "12.462.599,03" are the same number.
		if strings.LastIndex(raw, ".") > strings.LastIndex(raw, ",") {
			raw = strings.ReplaceAll(raw, ",", "")
		} else {
			raw = strings.ReplaceAll(raw, ".", "")
			raw = strings.ReplaceAll(raw, ",", ".")
		}
	case dots > 1:
		raw = strings.ReplaceAll(raw, ".", "") // "3.863.405.700"
	case commas > 1:
		raw = strings.ReplaceAll(raw, ",", "") // "3,863,405,700"
	case dots == 1 || commas == 1:
		sep := "."
		if commas == 1 {
			sep = ","
		}
		i := strings.Index(raw, sep)
		before, after := raw[:i], raw[i+1:]
		// A single separator with exactly three digits after it is a grouping
		// mark only when what precedes it is itself a group — "12.500" is
		// twelve thousand five hundred, but "1234.000" is a driver rendering a
		// DECIMAL and is 1234. Everything else is a decimal separator, which is
		// what makes "21,23" read as 21.23 instead of 2123.
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

// CollectNumbers pulls every numeric value out of a tool result, whatever
// shape it has.
//
// Depth- and count-bounded because a hundred-row result with twenty columns is
// two thousand numbers, and the comparison above is quadratic in this slice.
// The cap is not a sampling compromise: what the check needs is the figures a
// reply is likely to quote, and those are aggregates and the first rows of a
// result, which is what a bounded walk in document order returns.
func CollectNumbers(v any, max int) []float64 {
	var out []float64
	var walk func(any, int)
	walk = func(node any, depth int) {
		if len(out) >= max || depth > 6 {
			return
		}
		switch n := node.(type) {
		case float64:
			out = append(out, n)
		case int:
			out = append(out, float64(n))
		case int64:
			out = append(out, float64(n))
		case string:
			// Numbers arrive as strings from drivers that render DECIMAL that
			// way, which is every money column in this product's demo schema.
			if f, ok := parseLoose(n); ok {
				out = append(out, f)
			}
		case []any:
			for _, item := range n {
				walk(item, depth+1)
			}
		case map[string]any:
			for _, item := range n {
				walk(item, depth+1)
			}
		}
	}
	walk(v, 0)
	return out
}
