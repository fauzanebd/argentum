// Package eval scores the analytics agent against a fixed set of questions
// whose answers are known.
//
// Why it exists: six commits in this repo's history changed the system
// prompt or the model with no way to tell whether the change helped
// (finding Q-2). The T-00 smoke test then caught the agent reporting an
// invented sales figure — a failure invisible to `go test`, invisible in
// code review, and visible only by asking a question whose answer you
// already know. That is what this package does.
//
// The harness deliberately runs the *real* pipeline: the same agent factory,
// tools, guardrails and system prompt the worker uses, wired by
// internal/bootstrap, against a real tenant database. A mocked LLM would
// score a system nobody ships.
package eval

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Kind is what a case asserts about the agent's reply.
type Kind string

const (
	// KindNumeric asserts a specific number appears in the reply, within
	// tolerance, in any format the agent might reasonably use.
	KindNumeric Kind = "numeric"
	// KindContains asserts every listed phrase appears (case-insensitive).
	KindContains Kind = "contains"
	// KindSQLShape asserts the SQL the agent actually executed matches
	// every listed regexp — used where the number alone would not prove
	// the agent did the right thing.
	KindSQLShape Kind = "sql_shape"
	// KindRefusal asserts the agent declined, and did not touch the data.
	KindRefusal Kind = "refusal"
	// KindToolCalled asserts only that the right tools were or were not
	// called — for cases where the reply text is legitimately open-ended.
	KindToolCalled Kind = "tool_called"
)

// Case is one golden question.
type Case struct {
	ID       string `yaml:"id"`
	Question string `yaml:"question"`
	// Lang is the language the reply must be in ("en" or "id"). The agent
	// mixing languages is a real, shipped bug class — guideline 1 of the
	// system prompt exists because of it — so every case asserts it.
	Lang string `yaml:"lang"`
	// Category groups cases in the report ("simple_aggregate",
	// "time_window", "grouping_topn", "multi_source", "chart_dashboard",
	// "indonesian", "guardrail"). Coverage per category is the thing that
	// stops the set from drifting into thirty variations of one question.
	Category string `yaml:"category"`
	// Notes is free text for the human reading a failure.
	Notes string `yaml:"notes,omitempty"`
	// ReportFormat, when set, runs the case the way `POST /v1/reports` does:
	// the question is the caller's prompt and the report directive travels
	// beside it, out of the user message (T-A2b). It is a document format
	// ("pdf", "pptx", "xlsx", "csv") because that is what the directive names.
	//
	// It exists because the property T-A2b fixed is invisible to a plain case:
	// what the guardrail sees depends on how the turn was assembled, and the
	// only honest way to test it is to assemble the turn the same way the
	// route does.
	ReportFormat string `yaml:"report_format,omitempty"`
	Expect       Expect `yaml:"expect"`
}

// Expect is the assertion attached to a case.
type Expect struct {
	Kind Kind `yaml:"kind"`

	// Value + Tolerance apply to KindNumeric. Tolerance is relative
	// (0.01 = 1%); it defaults to 1% because the agent is asked to write
	// readable numbers, and "Rp 3,86 Miliar" is a correct answer to
	// 3_863_405_700.
	Value     float64 `yaml:"value,omitempty"`
	Tolerance float64 `yaml:"tolerance,omitempty"`

	// OrValues are additional numbers that also count as correct. Used
	// where a question has more than one defensible reading — "last month"
	// against a dataset ending 31 December 2024 can honestly mean December
	// (the last month with data) or November (the month before it). Both
	// are answers the agent derived from the database; an invented figure
	// still matches neither, which is the property that matters.
	OrValues []float64 `yaml:"or_values,omitempty"`

	// Contains applies to KindContains and KindRefusal: every phrase must
	// appear, case-insensitively.
	Contains []string `yaml:"contains,omitempty"`

	// NotContains fails the case if any phrase appears. Useful for
	// asserting the agent did not answer a question it should have
	// deflected.
	NotContains []string `yaml:"not_contains,omitempty"`

	// OrContains rescues a numeric case when the agent legitimately
	// declines to produce the number. "What were our total sales last
	// month?" has two correct answers against a dataset that stops in
	// December 2024: the December figure, or an explicit statement that
	// there is no data for last month. Both are honest; only inventing a
	// figure is not. Without this, the case would punish the honest
	// refusal, which is the exact behaviour the harness exists to reward.
	OrContains []string `yaml:"or_contains,omitempty"`

	// SQLMatches applies to KindSQLShape: every pattern must match at
	// least one executed statement.
	SQLMatches []string `yaml:"sql_matches,omitempty"`

	// NoFigure fails the case if the reply states a monetary or magnitude
	// figure at all. It exists for the questions whose only correct answer
	// is "there is no number" — a query that matched nothing, a turn that
	// ran out of budget — where no list of forbidden phrases can cover what
	// the agent might invent. Judged by the same rule that blocks the reply
	// in production (guardrails.StatesFigure), so the set and the guardrail
	// cannot drift apart on what counts as a figure.
	NoFigure bool `yaml:"no_figure,omitempty"`

	// MustCall / MustNotCall apply to every kind.
	MustCall    []string `yaml:"must_call,omitempty"`
	MustNotCall []string `yaml:"must_not_call,omitempty"`

	// MustCallAny passes when **at least one** of the named tools was called.
	//
	// It exists because `must_call: [run_sql]` on an aggregate question stopped
	// meaning what it was written to mean. The intent was always "the agent
	// went and got the number rather than inventing it"; `run_sql` was simply
	// the only tool that could. With the metric registry (T-06/T-07) defined,
	// the correct behaviour on "what were our total sales?" is `query_metric`,
	// so the old assertion fails a *better* answer — five cases did exactly
	// that on 2026-08-02. Naming both keeps the guarantee without pinning the
	// agent to the pre-registry tool choice.
	MustCallAny []string `yaml:"must_call_any,omitempty"`
}

// Set is a loaded golden file.
type Set struct {
	Name  string `yaml:"name"`
	Cases []Case `yaml:"cases"`
}

// LoadSet reads and validates a golden YAML file.
func LoadSet(path string) (*Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read golden set: %w", err)
	}
	var s Set
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse golden set: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate rejects a set that cannot mean what it says. A malformed case
// that silently passes is worse than a missing one.
func (s *Set) Validate() error {
	if len(s.Cases) == 0 {
		return fmt.Errorf("golden set has no cases")
	}
	seen := make(map[string]bool, len(s.Cases))
	for i := range s.Cases {
		c := &s.Cases[i]
		switch {
		case c.ID == "":
			return fmt.Errorf("case %d: id is required", i)
		case seen[c.ID]:
			return fmt.Errorf("case %q: duplicate id", c.ID)
		case c.Question == "":
			return fmt.Errorf("case %q: question is required", c.ID)
		case c.Lang != "en" && c.Lang != "id":
			return fmt.Errorf("case %q: lang must be 'en' or 'id', got %q", c.ID, c.Lang)
		case c.ReportFormat != "" && !domain.DocumentFormat(c.ReportFormat).Valid():
			return fmt.Errorf("case %q: report_format must be one of pdf, pptx, xlsx, csv, got %q", c.ID, c.ReportFormat)
		}
		seen[c.ID] = true

		switch c.Expect.Kind {
		case KindNumeric:
			if c.Expect.Tolerance == 0 {
				c.Expect.Tolerance = 0.01
			}
		case KindContains, KindRefusal:
			if len(c.Expect.Contains) == 0 && len(c.Expect.NotContains) == 0 &&
				len(c.Expect.MustCall) == 0 && len(c.Expect.MustNotCall) == 0 &&
				!c.Expect.NoFigure {
				return fmt.Errorf("case %q: kind %q asserts nothing", c.ID, c.Expect.Kind)
			}
		case KindSQLShape:
			if len(c.Expect.SQLMatches) == 0 {
				return fmt.Errorf("case %q: sql_shape needs sql_matches", c.ID)
			}
		case KindToolCalled:
			if len(c.Expect.MustCall) == 0 && len(c.Expect.MustNotCall) == 0 {
				return fmt.Errorf("case %q: tool_called needs must_call or must_not_call", c.ID)
			}
		default:
			return fmt.Errorf("case %q: unknown kind %q", c.ID, c.Expect.Kind)
		}
	}
	return nil
}

// Categories returns the case count per category, in no particular order.
func (s *Set) Categories() map[string]int {
	out := map[string]int{}
	for _, c := range s.Cases {
		name := c.Category
		if name == "" {
			name = "uncategorised"
		}
		out[name]++
	}
	return out
}

// Filter returns the subset whose id or category matches one of the given
// selectors. An empty selector list returns everything.
func (s *Set) Filter(selectors []string) []Case {
	if len(selectors) == 0 {
		return s.Cases
	}
	want := make(map[string]bool, len(selectors))
	for _, sel := range selectors {
		want[strings.TrimSpace(strings.ToLower(sel))] = true
	}
	out := make([]Case, 0, len(s.Cases))
	for _, c := range s.Cases {
		if want[strings.ToLower(c.ID)] || want[strings.ToLower(c.Category)] {
			out = append(out, c)
		}
	}
	return out
}
