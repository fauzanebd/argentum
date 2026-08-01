package metric

import (
	"fmt"
	"time"
)

// Window is the [From, To) a metric is measured over. The template decides
// whether it treats To as inclusive or exclusive (>= / < versus BETWEEN); this
// type only carries the two bounds and derives comparison windows from them.
type Window struct {
	From time.Time
	To   time.Time
}

// Comparison names how query_metric's optional second value is chosen (T-07).
type Comparison string

const (
	// ComparePreviousPeriod is the immediately preceding window of equal length:
	// last month against the month before it.
	ComparePreviousPeriod Comparison = "previous_period"
	// CompareSamePeriodLastYear is the same window shifted back one year, which
	// is how a seasonal business reads a number.
	CompareSamePeriodLastYear Comparison = "same_period_last_year"
)

// Valid reports whether c is a comparison this release computes.
func (c Comparison) Valid() bool {
	return c == ComparePreviousPeriod || c == CompareSamePeriodLastYear
}

// Shift returns the comparison window for w under c.
//
// previous_period abuts w on its left and has w's exact length, so the two are
// adjacent and equal — the only honest "vs. the period before". same_period_
// last_year steps both bounds back a calendar year with AddDate, which keeps
// month and day-of-month rather than subtracting 365 days, so February compares
// to February.
func (w Window) Shift(c Comparison) (Window, error) {
	switch c {
	case ComparePreviousPeriod:
		length := w.To.Sub(w.From)
		return Window{From: w.From.Add(-length), To: w.From}, nil
	case CompareSamePeriodLastYear:
		return Window{From: w.From.AddDate(-1, 0, 0), To: w.To.AddDate(-1, 0, 0)}, nil
	default:
		return Window{}, fmt.Errorf("unknown comparison %q", c)
	}
}

// ValidationWindow is the trailing-7-day window a metric is rendered and
// executed against when it is saved (T-06): recent enough to hit real data,
// wide enough that a daily-grain metric returns a row. `now` is passed in rather
// than read here so the check is deterministic in tests.
func ValidationWindow(now time.Time) Window {
	return Window{From: now.AddDate(0, 0, -7), To: now}
}
