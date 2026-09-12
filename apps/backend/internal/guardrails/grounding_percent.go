package guardrails

import (
	"math"
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/numparse"
)

// percentInProse finds a figure written as a percentage, in both languages
// this product answers in: `18.42%`, `18,42 %`, `18,42 persen`, `18 percent`.
var percentInProse = regexp.MustCompile(`(?i)(\d[\d.,]*)\s*(?:%|persen\b|percent\b)`)

// statedPercent is one percentage a reply asserts.
type statedPercent struct {
	value float64
	// halfStep is half of the last unit the reply wrote: 0.005 for "18.42%",
	// 0.5 for "18%". A returned 18.4211 written as "18%" is a correct rounding,
	// and the stated precision is the only honest tolerance for it — one
	// percent relative of 18 is 0.18, which would call that rounding a
	// different number, while the exact tolerance would call "18.42%" wrong.
	halfStep float64
}

// groundPercents fills the report's two percentage fields (T-W3). Only ever
// called on a report that is Checked, for Checked's reason.
func groundPercents(rep *GroundingReport, reply string, returned []float64) {
	for _, pc := range extractStatedPercents(reply) {
		rep.StatedPercents = append(rep.StatedPercents, pc.value)
		if !percentGrounded(pc, returned) {
			rep.UngroundedPercents = append(rep.UngroundedPercents, pc.value)
		}
	}
}

// extractStatedPercents pulls the percentages out of a reply's prose — the
// same stripped prose extractStatedFigures reads, so a percentage inside a
// code block or a link is not a claim.
func extractStatedPercents(reply string) []statedPercent {
	prose := stripNonProse(reply)
	seen := map[float64]bool{}
	var out []statedPercent
	for _, m := range percentInProse.FindAllStringSubmatch(prose, -1) {
		tok := strings.Trim(m[1], ".,")
		v, ok := numparse.Parse(tok)
		if !ok || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, statedPercent{value: v, halfStep: halfStepOf(v)})
	}
	return out
}

// halfStepOf is half the smallest decimal place a parsed value carries.
//
// Read off the value rather than the token, because the token's separators are
// ambiguous until numparse has decided them — "18,42" is two decimal places and
// "12.500" is none. A reply writing "18.40%" gets 0.05 rather than 0.005, since
// the trailing zero does not survive the parse; that is a looser tolerance in
// the direction of calling a figure grounded, which is the direction an
// instrument that reports rather than blocks can afford.
func halfStepOf(v float64) float64 {
	step := 1.0
	for i := 0; i < 6; i++ {
		scaled := v / step
		if math.Abs(scaled-math.Round(scaled)) < 1e-6 {
			break
		}
		step /= 10
	}
	return step / 2
}

// percentGrounded reports whether a stated percentage is one a tool returned,
// either as written or as the fraction it renders. `compute` returning
// "0.1842" and a reply writing "18.42%" is the ordinary case, and so is a
// query selecting `round(100.0 * a / b, 2)`.
func percentGrounded(p statedPercent, returned []float64) bool {
	for _, got := range returned {
		for _, candidate := range [2]float64{got, got * 100} {
			if math.Abs(p.value-candidate) <= p.halfStep+1e-9 {
				return true
			}
		}
	}
	return false
}
