package domain

import (
	"context"
	"time"
)

// MetricCoverage is how many of a company's answered turns stood on a defined
// metric rather than on SQL the model wrote for the occasion (T-F4).
//
// The question behind it is the one the market now uses to decide whether an AI
// answer can be trusted unattended: can it be traced back to a certified
// definition, or was the number re-derived? `03-gap-analysis.md` made the same
// argument about this product before the registry existed — that the metric
// layer is the moat, because a competitor can clone a chat UI in a week and
// cannot clone an accumulated, curated metric layer. Nothing has ever measured
// whether it is accumulating.
//
// **A turn is classified, not a call.** Counting calls would score one turn
// that ran query_metric five times above five turns that each ran run_sql once,
// which is backwards: the unit a customer cares about is the answer.
type MetricCoverage struct {
	// From is the start of the window these counts cover.
	From time.Time `json:"from"`
	// Certified turns used defined metrics and no ad-hoc SQL.
	Certified int `json:"certified"`
	// AdHoc turns re-derived their numbers with run_sql and used no metric.
	AdHoc int `json:"ad_hoc"`
	// Mixed turns did both. Kept as its own bucket rather than folded into
	// either: a turn that read a metric and then joined something to it is a
	// different thing from one that ignored the registry, and calling it a win
	// or a loss would be a judgement this number should not be making.
	Mixed int `json:"mixed"`
	// NoData turns called tools but neither of the two data tools — a document
	// search, a chart, a scheduled task. Reported rather than dropped, because a
	// denominator that quietly excludes them makes the percentage unreadable.
	NoData int `json:"no_data"`
}

// Answered is the turns that produced a number one way or the other. The
// denominator of the percentage, and deliberately not NoData + the rest: a turn
// that never asked the data anything says nothing about whether the registry
// covers the questions people ask.
func (c MetricCoverage) Answered() int { return c.Certified + c.AdHoc + c.Mixed }

// Percent is the share of answering turns that stood on a definition, counting
// a mixed turn as covered — it did reach the registry. Zero when nothing was
// answered, which reads as "no data" rather than as 0% on a screen that knows
// the difference.
func (c MetricCoverage) Percent() float64 {
	n := c.Answered()
	if n == 0 {
		return 0
	}
	return float64(c.Certified+c.Mixed) / float64(n) * 100
}

// AdHocQuestion is a question this company answered with ad-hoc SQL, and how
// often. The part of the coverage panel an admin can act on: it names what to
// define next.
type AdHocQuestion struct {
	Question string `json:"question"`
	Turns    int    `json:"turns"`
	LastSQL  string `json:"last_sql,omitempty"`
}

// MetricCoverageReader is the persistence contract for both halves.
type MetricCoverageReader interface {
	Coverage(ctx context.Context, companyID string, since time.Time) (MetricCoverage, error)
	TopAdHocQuestions(ctx context.Context, companyID string, since time.Time, limit int) ([]AdHocQuestion, error)
}
