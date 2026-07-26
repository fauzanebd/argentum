package eval

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Result is one scored case.
type Result struct {
	ID       string `json:"id"`
	Category string `json:"category,omitempty"`
	Question string `json:"question"`
	ThreadID string `json:"thread_id,omitempty"`

	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
	// Error is set when the turn itself failed (provider error, timeout)
	// rather than the answer being wrong.
	Error string `json:"error,omitempty"`

	Reply     string           `json:"reply"`
	ToolCalls []ToolInvocation `json:"tool_calls,omitempty"`

	DurationMS int64   `json:"duration_ms"`
	TokensIn   int64   `json:"tokens_in"`
	TokensOut  int64   `json:"tokens_out"`
	CostUSD    float64 `json:"cost_usd"`
	LLMCalls   int64   `json:"llm_calls"`
}

// Report is a whole run.
type Report struct {
	Set       string    `json:"set"`
	Model     string    `json:"model"`
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration"`

	Total   int     `json:"total"`
	Passed  int     `json:"passed"`
	Failed  int     `json:"failed"`
	Errored int     `json:"errored"`
	PassRat float64 `json:"pass_rate"`

	MeanTokensIn  float64 `json:"mean_tokens_in"`
	MeanTokensOut float64 `json:"mean_tokens_out"`
	MeanLatencyMS float64 `json:"mean_latency_ms"`
	MeanCostUSD   float64 `json:"mean_cost_usd"`
	TotalCostUSD  float64 `json:"total_cost_usd"`

	ByCategory map[string]CategoryScore `json:"by_category"`
	Results    []Result                 `json:"results"`
}

// CategoryScore is the per-category roll-up. Aggregate pass rate hides the
// thing worth knowing: a set can look healthy at 80% while every Indonesian
// case fails.
type CategoryScore struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`
}

// Summarize computes aggregates over finished results.
func Summarize(setName, model string, startedAt time.Time, results []Result) Report {
	rep := Report{
		Set:        setName,
		Model:      model,
		StartedAt:  startedAt,
		Duration:   time.Since(startedAt).Round(time.Second).String(),
		Total:      len(results),
		ByCategory: map[string]CategoryScore{},
		Results:    results,
	}
	if len(results) == 0 {
		return rep
	}

	var sumIn, sumOut, sumLatency int64
	var sumCost float64
	for _, r := range results {
		if r.Passed {
			rep.Passed++
		} else {
			rep.Failed++
		}
		if r.Error != "" {
			rep.Errored++
		}
		sumIn += r.TokensIn
		sumOut += r.TokensOut
		sumLatency += r.DurationMS
		sumCost += r.CostUSD

		cat := r.Category
		if cat == "" {
			cat = "uncategorised"
		}
		cs := rep.ByCategory[cat]
		cs.Total++
		if r.Passed {
			cs.Passed++
		}
		rep.ByCategory[cat] = cs
	}

	n := float64(len(results))
	rep.PassRat = float64(rep.Passed) / n
	rep.MeanTokensIn = float64(sumIn) / n
	rep.MeanTokensOut = float64(sumOut) / n
	rep.MeanLatencyMS = float64(sumLatency) / n
	rep.MeanCostUSD = sumCost / n
	rep.TotalCostUSD = sumCost

	for cat, cs := range rep.ByCategory {
		cs.PassRate = float64(cs.Passed) / float64(cs.Total)
		rep.ByCategory[cat] = cs
	}
	return rep
}

// Text renders the human-facing summary — this is what a ticket gate pastes.
func (r Report) Text() string {
	var b strings.Builder

	fmt.Fprintf(&b, "\n=== Argentum eval — %s ===\n", r.Set)
	fmt.Fprintf(&b, "model:      %s\n", r.Model)
	fmt.Fprintf(&b, "started:    %s\n", r.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "duration:   %s\n\n", r.Duration)

	fmt.Fprintf(&b, "PASS RATE:  %.1f%%  (%d/%d)\n", r.PassRat*100, r.Passed, r.Total)
	if r.Errored > 0 {
		fmt.Fprintf(&b, "            %d case(s) errored rather than answered — see below\n", r.Errored)
	}
	fmt.Fprintf(&b, "mean in:    %.0f tokens\n", r.MeanTokensIn)
	fmt.Fprintf(&b, "mean out:   %.0f tokens\n", r.MeanTokensOut)
	fmt.Fprintf(&b, "mean lat:   %.0f ms\n", r.MeanLatencyMS)
	fmt.Fprintf(&b, "mean cost:  $%.6f\n", r.MeanCostUSD)
	fmt.Fprintf(&b, "total cost: $%.6f\n", r.TotalCostUSD)

	if r.MeanTokensIn == 0 && r.MeanTokensOut == 0 {
		b.WriteString("\n! Zero tokens recorded across every case. The turns ran, so this is\n" +
			"  the metering gap from finding Q-12 (T-02c), not an empty run.\n")
	}

	b.WriteString("\n--- by category ---\n")
	cats := make([]string, 0, len(r.ByCategory))
	for c := range r.ByCategory {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		cs := r.ByCategory[c]
		fmt.Fprintf(&b, "  %-18s %5.1f%%  (%d/%d)\n", c, cs.PassRate*100, cs.Passed, cs.Total)
	}

	failed := make([]Result, 0, r.Failed)
	for _, res := range r.Results {
		if !res.Passed {
			failed = append(failed, res)
		}
	}
	if len(failed) > 0 {
		b.WriteString("\n--- failures ---\n")
		for _, res := range failed {
			fmt.Fprintf(&b, "\n  %s [%s]\n", res.ID, res.Category)
			fmt.Fprintf(&b, "    Q: %s\n", res.Question)
			for _, f := range res.Failures {
				fmt.Fprintf(&b, "    ✗ %s\n", f)
			}
			fmt.Fprintf(&b, "    tools: %s\n", describeCalls(res.ToolCalls))
			fmt.Fprintf(&b, "    reply: %s\n", truncate(oneLine(res.Reply), 300))
		}
	}
	b.WriteString("\n")
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
