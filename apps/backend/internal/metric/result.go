package metric

import (
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Evaluation is the result of running a metric over one window: the number, the
// window it covered, and the SQL it ran (for the dashboard's Test button and a
// support trace). It lives in this package rather than in the app service so the
// query_metric tool (internal/tools) can name it without importing internal/app,
// which would be a cycle.
type Evaluation struct {
	Value       float64   `json:"value"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	RenderedSQL string    `json:"rendered_sql"`
	// Empty reports that the template ran and matched nothing: every dialect
	// answers an aggregate over an empty set with one row whose value column is
	// NULL, and that is a different fact from a zero. Value is 0 when this is
	// set and means nothing — read this field first.
	//
	// It exists because the two sentences differ to a customer. Asked for Q3
	// 2025 against a warehouse that stops in December 2024, query_metric
	// answered "Rp 0" (docs/coverage/eval-q1.md); "sales were zero" and "we
	// hold no data for that period" are both short, and only one of them is
	// true. run_sql was given the same distinction by T-Q9 on 2026-08-11 and
	// the metric path was never given it.
	Empty bool `json:"empty,omitempty"`
	// Zero carries what was found out about a value of exactly 0, and is set
	// only for one. See ZeroCoverage.
	Zero *ZeroCoverage `json:"zero_coverage,omitempty"`
}

// ZeroVerdict is what a metric's 0 turned out to mean.
type ZeroVerdict string

const (
	// ZeroUnknown is the answer when the probe did not run — switched off, or
	// it errored. The caller keeps the hedged note this type exists to replace.
	ZeroUnknown ZeroVerdict = ""
	// ZeroInsideCoverage: the metric returns a non-zero value both before and
	// after the requested window, so the data reaches this period and the 0 is
	// the real total.
	ZeroInsideCoverage ZeroVerdict = "inside_coverage"
	// ZeroAfterCoverage: there are values before the window and none after it.
	// The window is at or past the end of what the data holds — the Q3-2025
	// case against a warehouse that stops in December 2024.
	ZeroAfterCoverage ZeroVerdict = "after_coverage"
	// ZeroBeforeCoverage: values after the window, none before. The window
	// starts earlier than the data does.
	ZeroBeforeCoverage ZeroVerdict = "before_coverage"
	// ZeroEverywhere: no non-zero value on either side. The metric is 0 (or
	// matches nothing) over everything the data holds, which is a definition or
	// loading problem rather than an answer about a period.
	ZeroEverywhere ZeroVerdict = "everywhere"
)

// ZeroCoverage is what two extra queries established about a value of exactly 0.
//
// **Why a metric needs this and run_sql does not.** An aggregate over no rows
// returns NULL, which `Evaluation.Empty` reports and which is unambiguous. But
// a metric template is written `COALESCE(SUM(x), 0)` — as it should be, or the
// registry's own validation would reject a perfectly good definition the first
// quiet week — and that COALESCE converts the unambiguous NULL into a 0 that
// means either "no rows here" or "the rows here sum to nothing". `run_sql` got
// this distinction from T-Q9 in August 2026; the metric path was left with a
// sentence asking the model to hedge, and both models duly reported **Rp 0**
// for a quarter the warehouse does not reach.
//
// **What is measured, and what is inferred.** The two probes run the same
// metric over everything before the window and everything after it. A non-zero
// value on a side is proof of data on that side. A zero on a side proves
// nothing on its own — it has the identical ambiguity — which is why the
// verdicts are phrased as what was observed rather than as where the data
// begins and ends. Two facts about the sides are enough to answer the only
// question that reaches a customer: is this window inside the data or outside
// it.
type ZeroCoverage struct {
	Verdict ZeroVerdict `json:"verdict"`
	// Before and After are the metric's own values over the windows either side
	// of the one that was asked for. Nil when that side was not probed or came
	// back empty; the numbers are carried so a support conversation can see
	// what the verdict was drawn from.
	Before *float64 `json:"before,omitempty"`
	After  *float64 `json:"after,omitempty"`
}

// Result is what query_metric returns (T-07): the primary value, and — when a
// comparison was asked for — the comparison value plus the delta between them.
type Result struct {
	Metric     *domain.MetricDefinition
	Primary    Evaluation
	Comparison *Evaluation
	// Delta and DeltaPct are Primary − Comparison, present only when a comparison
	// ran. DeltaPct is nil when the comparison value was zero (a percentage
	// change from nothing is not a number).
	Delta    *float64
	DeltaPct *float64
}
