package domain

import (
	"context"
	"time"
)

// DerivedFigures is how often a company's answers stated a figure no tool
// returned, and how often they did so after `compute` was called (T-W3).
//
// It is the measurement that decides whether `T-W4`'s sandbox is worth three
// days. `compute` closes the margin case — two figures from two queries, one
// ratio — without executing anything. What it cannot express is a loop, a
// reconciliation, or arithmetic across two sources, and nobody knew how often
// this deployment meets those. Residue is the bucket that says a program might
// be needed; CrossSource is the one class of the three that has a structural
// signature at all.
//
// **A turn is classified, not a call**, for MetricCoverage's reason: the unit a
// customer experiences is the answer.
type DerivedFigures struct {
	// From is the start of the window these counts cover.
	From time.Time `json:"from"`

	// Answered is every turn that called a data tool successfully. The
	// denominator CrossSource is read against.
	Answered int `json:"answered"`
	// Checked is the subset whose reply carries a grounding record — the only
	// turns about which "did it state a figure no tool returned" has an answer.
	// **It is smaller than Answered and the gap is not noise.** Nothing stored a
	// turn's grounding verdict before this ticket, so every earlier turn is
	// unchecked; so is a blocking run, which produces no tool events to check
	// against. Reported so the three buckets below are never read against the
	// wrong denominator.
	Checked int `json:"checked"`

	// Composed turns stated a figure no tool returned and did not call
	// `compute`: arithmetic done in the sentence. The class T-W1 exists for.
	Composed int `json:"composed"`
	// Computed turns called `compute` and stated nothing that it — or another
	// data tool — did not return. Covered.
	Computed int `json:"computed"`
	// Residue turns called `compute` and then stated a further figure no tool
	// returned anyway. **This is the number that justifies T-W4**, and it is
	// counted apart from Composed because the two call for different fixes: a
	// Composed turn needed a tool it did not reach for, a Residue turn reached
	// for one and it was not enough — or was not used for everything.
	Residue int `json:"residue"`

	// CrossSource turns' data tools read from two or more different sources in
	// one turn — research §3d's first class, and the one that needs no new
	// instrumentation, because `agent_actions.source_id` already says it.
	CrossSource int `json:"cross_source"`
}

// Unchecked is how many answering turns carry no grounding record.
func (d DerivedFigures) Unchecked() int {
	if n := d.Answered - d.Checked; n > 0 {
		return n
	}
	return 0
}

// ComputeTurns is every checked turn that called `compute`, clean or not.
func (d DerivedFigures) ComputeTurns() int { return d.Computed + d.Residue }

// UnaccountedPercent is the share of checked turns that stated a figure no
// tool returned, with `compute` or without it. Zero when nothing was checked,
// which reads as "no data" rather than as 0% on a screen that knows the
// difference.
func (d DerivedFigures) UnaccountedPercent() float64 {
	if d.Checked == 0 {
		return 0
	}
	return float64(d.Composed+d.Residue) / float64(d.Checked) * 100
}

// ResiduePercent is the share of compute turns that still stated a figure
// nothing returned. Zero when no checked turn called `compute`.
func (d DerivedFigures) ResiduePercent() float64 {
	n := d.ComputeTurns()
	if n == 0 {
		return 0
	}
	return float64(d.Residue) / float64(n) * 100
}

// TurnShape is one combination of the four facts T-W3 reads about a turn, and
// how many turns had it.
//
// The repository returns these rather than the buckets, so which combination
// lands in which bucket is decided in Go — by TallyDerivedFigures, where a test
// can reach it. Four booleans are at most sixteen rows, so nothing is lost by
// moving the arithmetic out of SQL, and the classification is the part of this
// ticket most likely to be argued with.
type TurnShape struct {
	// Computed: at least one successful `compute` call.
	Computed bool `json:"computed"`
	// CrossSource: the turn's data tools read two or more sources.
	CrossSource bool `json:"cross_source"`
	// Checked: the reply carries a grounding record.
	Checked bool `json:"checked"`
	// Unaccounted: that record lists a figure or a percentage no tool returned.
	Unaccounted bool `json:"unaccounted"`
	Turns       int  `json:"turns"`
}

// TallyDerivedFigures folds turn shapes into the panel's buckets.
//
// An unchecked turn is counted in Answered and, when it qualifies, CrossSource
// — both of which `agent_actions` alone can answer — and in none of the three
// buckets that need to know what the reply said. That includes an unchecked
// turn that called `compute`: without a record, "it computed and stated nothing
// else" and "it computed and then divided anyway" look identical, and guessing
// would put a turn nobody measured into the number that decides T-W4.
func TallyDerivedFigures(from time.Time, shapes []TurnShape) DerivedFigures {
	out := DerivedFigures{From: from}
	for _, s := range shapes {
		out.Answered += s.Turns
		if s.CrossSource {
			out.CrossSource += s.Turns
		}
		if !s.Checked {
			continue
		}
		out.Checked += s.Turns
		switch {
		case s.Computed && s.Unaccounted:
			out.Residue += s.Turns
		case s.Computed:
			out.Computed += s.Turns
		case s.Unaccounted:
			out.Composed += s.Turns
		}
	}
	return out
}

// DerivedFiguresReader is the persistence contract.
//
// dataTools is passed rather than written into the query, so the definition of
// "a turn that asked the data something" is agentbudget's — the same list that
// decides what grounds a figure — and cannot drift from it the day a fifth data
// tool is added.
type DerivedFiguresReader interface {
	TurnShapes(ctx context.Context, companyID string, since time.Time, dataTools []string) ([]TurnShape, error)
}
