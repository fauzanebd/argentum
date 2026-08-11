package eval

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/report/format"
)

// ToolInvocation is one tool the agent called during a turn, as observed on
// the event bus.
type ToolInvocation struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// Score evaluates one turn against its expectation and returns the list of
// reasons it failed. An empty list is a pass.
//
// Every check that can run, runs — a case reports all of its problems at
// once rather than the first, because chasing them one per eval run costs
// minutes of LLM latency each time.
func Score(c Case, reply string, calls []ToolInvocation) []string {
	var failures []string

	if strings.TrimSpace(reply) == "" {
		return []string{"empty reply"}
	}

	switch c.Expect.Kind {
	case KindNumeric:
		failures = append(failures, scoreNumeric(c, reply)...)
	case KindContains:
		// handled by the shared contains checks below
	case KindSQLShape:
		failures = append(failures, scoreSQLShape(c, calls)...)
	case KindRefusal:
		failures = append(failures, scoreRefusal(c, calls)...)
	case KindToolCalled:
		// handled by the shared tool checks below
	}

	failures = append(failures, scoreContains(c, reply)...)
	failures = append(failures, scoreNoFigure(c, reply)...)
	failures = append(failures, scoreToolCalls(c, calls)...)
	failures = append(failures, scoreLanguage(c, reply)...)

	return failures
}

// scoreNoFigure fails a case whose only honest answer is "there is no
// number" when the reply states one anyway. Delegates to the production
// guardrail so a change to what counts as a figure moves both at once.
func scoreNoFigure(c Case, reply string) []string {
	if !c.Expect.NoFigure || !guardrails.StatesFigure(reply) {
		return nil
	}
	return []string{"reply states a figure, and there is no figure to state"}
}

func scoreNumeric(c Case, reply string) []string {
	want := c.Expect.Value
	tol := c.Expect.Tolerance
	if tol == 0 {
		tol = 0.01
	}
	// An explicit "I don't have data for that" is a pass on cases that
	// allow it — see Expect.OrContains.
	lower := strings.ToLower(reply)
	for _, alt := range c.Expect.OrContains {
		if strings.Contains(lower, strings.ToLower(alt)) {
			return nil
		}
	}
	nums := format.ExtractNumbers(reply)
	if len(nums) == 0 {
		return []string{fmt.Sprintf("expected %.2f, reply contains no numbers", want)}
	}
	accepted := append([]float64{want}, c.Expect.OrValues...)
	for _, n := range nums {
		for _, target := range accepted {
			if format.Matches(n, target, tol) {
				return nil
			}
		}
	}
	// Report the closest miss: it is the difference between "off by a
	// rounding" and "invented a placeholder", which is the distinction the
	// whole harness exists to make.
	closest := nums[0]
	closestErr := relErr(nums[0].Value, want)
	for _, n := range nums[1:] {
		if e := relErr(n.Value, want); e < closestErr {
			closest, closestErr = n, e
		}
	}
	return []string{fmt.Sprintf(
		"expected %.2f (±%.1f%%), closest number in reply was %.2f from %q — off by %.1f%%",
		want, tol*100, closest.Value, closest.Raw, closestErr*100,
	)}
}

func relErr(got, want float64) float64 {
	if want == 0 {
		return got
	}
	d := got - want
	if d < 0 {
		d = -d
	}
	if want < 0 {
		want = -want
	}
	return d / want
}

func scoreContains(c Case, reply string) []string {
	var failures []string
	lower := strings.ToLower(reply)
	for _, want := range c.Expect.Contains {
		if !strings.Contains(lower, strings.ToLower(want)) {
			failures = append(failures, fmt.Sprintf("reply does not contain %q", want))
		}
	}
	for _, unwanted := range c.Expect.NotContains {
		if strings.Contains(lower, strings.ToLower(unwanted)) {
			failures = append(failures, fmt.Sprintf("reply contains %q and should not", unwanted))
		}
	}
	if len(c.Expect.ContainsAny) > 0 {
		found := false
		for _, want := range c.Expect.ContainsAny {
			if strings.Contains(lower, strings.ToLower(want)) {
				found = true
				break
			}
		}
		if !found {
			failures = append(failures, fmt.Sprintf(
				"reply contains none of %q", strings.Join(c.Expect.ContainsAny, ", ")))
		}
	}
	return failures
}

func scoreSQLShape(c Case, calls []ToolInvocation) []string {
	var statements []string
	for _, call := range calls {
		if call.Name != "run_sql" {
			continue
		}
		if q, ok := call.Args["query"].(string); ok {
			statements = append(statements, q)
		}
		if q, ok := call.Args["sql"].(string); ok {
			statements = append(statements, q)
		}
	}
	if len(statements) == 0 {
		return []string{"sql_shape: agent executed no SQL"}
	}
	var failures []string
	for _, pattern := range c.Expect.SQLMatches {
		re, err := regexp.Compile("(?is)" + pattern)
		if err != nil {
			failures = append(failures, fmt.Sprintf("sql_shape: bad pattern %q: %v", pattern, err))
			continue
		}
		matched := false
		for _, stmt := range statements {
			if re.MatchString(stmt) {
				matched = true
				break
			}
		}
		if !matched {
			failures = append(failures, fmt.Sprintf(
				"sql_shape: no statement matched %q (executed: %s)",
				pattern, strings.Join(statements, " | "),
			))
		}
	}
	return failures
}

// scoreRefusal checks the shape of a guardrail refusal: the agent must not
// have touched tenant data. The specific wording is asserted per-case via
// Contains, because the messages live in config/guardrails.yaml and a test
// that hardcodes them here would drift from the config.
func scoreRefusal(c Case, calls []ToolInvocation) []string {
	var failures []string
	for _, call := range calls {
		switch call.Name {
		case "run_sql", "create_visualization", "create_dashboard", "generate_document":
			failures = append(failures, fmt.Sprintf(
				"refusal expected but the agent called %s", call.Name))
		}
	}
	return failures
}

func scoreToolCalls(c Case, calls []ToolInvocation) []string {
	called := make(map[string]bool, len(calls))
	for _, call := range calls {
		called[call.Name] = true
	}
	var failures []string
	for _, want := range c.Expect.MustCall {
		if !called[want] {
			failures = append(failures, fmt.Sprintf("expected a %s call, got %s", want, describeCalls(calls)))
		}
	}
	if len(c.Expect.MustCallAny) > 0 {
		var any bool
		for _, want := range c.Expect.MustCallAny {
			if called[want] {
				any = true
				break
			}
		}
		if !any {
			failures = append(failures, fmt.Sprintf("expected one of %s, got %s",
				strings.Join(c.Expect.MustCallAny, "/"), describeCalls(calls)))
		}
	}
	for _, unwanted := range c.Expect.MustNotCall {
		if called[unwanted] {
			failures = append(failures, fmt.Sprintf("called %s and should not have", unwanted))
		}
	}
	return failures
}

func scoreLanguage(c Case, reply string) []string {
	got := DetectLanguage(reply)
	if got == "" || got == c.Lang {
		return nil
	}
	return []string{fmt.Sprintf("replied in %q, expected %q", got, c.Lang)}
}

func describeCalls(calls []ToolInvocation) string {
	if len(calls) == 0 {
		return "no tool calls"
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}
