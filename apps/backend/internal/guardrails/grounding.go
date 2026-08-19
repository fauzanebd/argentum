package guardrails

import (
	"math"
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/numparse"
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

// ungroundedTolerance is how close a *rendered* figure has to be to a returned
// one to count as the same number.
//
// One percent, matching the eval harness's default numeric tolerance and for
// the same reason: the agent is instructed to write readable numbers, so
// "Rp 3,86 Miliar" is the correct rendering of 3,863,405,700 and must not read
// as a different figure.
const ungroundedTolerance = 0.01

// exactTolerance is what a figure written at full precision is held to (T-Q14).
//
// **The gap this closes was found by a gate, not by a test.** A turn printed
// December revenue as `$3,860,405,700.00` where its own run_sql returned
// `3,863,405,700.00`. The misquote is 0.078% — inside one percent — so the
// reply reported clean, while the same turn's *derived* quarter total was
// flagged. One percent of a billion is ten million.
//
// **Lowering ungroundedTolerance was the wrong fix and this is why.** That
// tolerance exists because the system prompt *requires* magnitude rendering, so
// "Rp 3,86 Miliar" is the correct way to write 3,863,405,700 — tighten it and
// every correctly formatted Indonesian answer reports as ungrounded, which is
// the instrument becoming noise on half the traffic.
//
// So the two jobs are separated instead. A figure written to the cent, or with
// its thousands grouped, is making an *exact* claim and is matched exactly. A
// figure written in magnitude units is making an *approximate* one and keeps
// the tolerance it needs. Near-zero rather than zero because both sides went
// through a float parse: 1e-9 relative is the width of that parse, not a
// judgement about accuracy.
const exactTolerance = 1e-9

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
	stated := extractStatedFigures(reply)

	rep.Stated = make([]float64, 0, len(stated))
	for _, f := range stated {
		rep.Stated = append(rep.Stated, f.value)
		if !grounded(f, returned) {
			rep.Ungrounded = append(rep.Ungrounded, f.value)
		}
	}
	return rep
}

// statedFigure is one number a reply asserts, and how precisely it was written.
type statedFigure struct {
	value float64
	// exact says the reply wrote this number at full precision — grouped
	// thousands, or digits to the cent, with no magnitude word beside it. Such a
	// figure is quoted rather than rendered, and a reader will quote it onward,
	// so it is held to exactTolerance. See T-Q14.
	exact bool
}

// magnitudeWord matches the units a rendered figure is written in, in both
// languages this product answers in. `bn`/`m`/`k` are here because a model
// asked for English sometimes writes "Rp 3.86bn" — and the abbreviations are
// anchored to a word boundary so the `m` in "3,000 meters" is not one.
var magnitudeWord = regexp.MustCompile(
	`(?i)^\s*(thousand|million|billion|trillion|ribu|juta|miliar|milyar|triliun|bn|tn|k|m)\b`)

// extractStatedFigures pulls the numbers out of a reply's prose along with how
// precisely each was written.
//
// It walks match *positions* rather than match strings, because the classifying
// question — is a magnitude word sitting immediately after this number? — is
// about the text around the token and is unanswerable once the token has been
// cut out of it.
func extractStatedFigures(reply string) []statedFigure {
	prose := stripNonProse(reply)
	seen := map[float64]bool{}
	var out []statedFigure
	for _, loc := range figureInProse.FindAllStringIndex(prose, -1) {
		raw := prose[loc[0]:loc[1]]
		// figureInProse ends at `[\d.,]*`, so a figure that closes a sentence
		// arrives with the full stop attached — "…31 December 2024." matches as
		// `2024.`. numparse.Parse trims that before parsing; isBareYear below reads
		// the token's punctuation to tell a year from a quantity, so it has to
		// be handed the same trimmed token or every sentence-final year looks
		// like a decimal. Trimming once, here, keeps the two in agreement.
		tok := strings.Trim(raw, ".,")
		v, ok := numparse.Parse(tok)
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
		out = append(out, statedFigure{value: v, exact: writtenExactly(prose[loc[1]:])})
	}
	return out
}

// writtenExactly reports whether this figure is quoted rather than rendered.
//
// One question decides it: is a magnitude word sitting immediately after the
// digits? "Rp 3,86 Miliar" is the correct way to write 3,863,405,700 under this
// product's own system prompt, so it is an approximation on purpose and keeps
// the one-percent tolerance.
//
// Everything else is the writer stating the number in full — grouped thousands,
// digits to the cent, or a bare run of digits — and a reader will quote it
// onward exactly as written.
func writtenExactly(after string) bool {
	return !magnitudeWord.MatchString(after)
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
func grounded(want statedFigure, returned []float64) bool {
	// The direct comparison is where the two tolerances differ (T-Q14): a figure
	// the reply wrote out in full has to *be* one of the returned numbers, while
	// a rendered one only has to round to it.
	tol := ungroundedTolerance
	if want.exact {
		tol = exactTolerance
	}
	for _, got := range returned {
		if within(want.value, got, tol) {
			return true
		}
		// Magnitude rendering is only ever a question about a rendered figure —
		// an exact one is not claiming to be 3.86 of anything.
		if !want.exact && sameMagnitudeRendering(want.value, got) {
			return true
		}
	}
	// A sum or a difference of two returned values, at the loose tolerance
	// **whatever the figure looks like**. A derived total is arithmetic the model
	// did, over numbers a driver may have rounded on the way out, and holding it
	// to the exact tolerance would flag every correct quarter total written to
	// the cent. Quadratic, and bounded by the cap the caller applies to
	// `returned` — a turn's tool results are tens of numbers, not thousands.
	for i := range returned {
		for j := range returned {
			if i == j {
				continue
			}
			if closeEnough(want.value, returned[i]+returned[j]) ||
				closeEnough(want.value, returned[i]-returned[j]) {
				return true
			}
		}
	}
	return false
}

// closeEnough is the loose comparison: a rendered figure, or a derived one.
func closeEnough(a, b float64) bool { return within(a, b, ungroundedTolerance) }

func within(a, b, tol float64) bool {
	if a == b {
		return true
	}
	if b == 0 {
		return math.Abs(a) < 1e-9
	}
	return math.Abs(a-b)/math.Abs(b) <= tol
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

// CollectNumbersInProse is CollectNumbers for a tool whose results are
// sentences rather than rows (T-P9).
//
// `search_documents` returns retrieved chunks: paragraphs of a contract, a
// policy or a report. A figure inside one of them — *"denda keterlambatan
// sebesar Rp 5.000.000 per hari"* — is a number the document really states and
// the model may legitimately quote, but CollectNumbers cannot see it, because
// it reads a string as one value and that string is a paragraph.
//
// Without this, every figure quoted out of a retrieved document would report as
// ungrounded and the counter this product spent three sittings building would
// become noise on exactly the feature most likely to produce a misquote. With
// it, a figure the agent retrieved is checkable against the chunk that carried
// it, and a figure from a document nobody retrieved stays ungrounded — which is
// correct, and is the whole reason retrieval is a tool call rather than an
// injection.
//
// **It is deliberately not the default.** Scanning inside every string of every
// tool result would collect the digits out of table names, error messages and
// SQL text, and each one of those is a number that would ground a fabrication.
func CollectNumbersInProse(v any, max int) []float64 {
	out := CollectNumbers(v, max)
	if len(out) >= max {
		return out
	}
	var walk func(any, int)
	walk = func(node any, depth int) {
		if len(out) >= max || depth > 6 {
			return
		}
		switch n := node.(type) {
		case string:
			for _, f := range extractStatedFigures(n) {
				if len(out) >= max {
					return
				}
				out = append(out, f.value)
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
			if f, ok := numparse.Parse(n); ok {
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
