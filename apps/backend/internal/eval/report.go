package eval

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/llmusage"
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

	// Reply and ToolCalls are the LAST turn's — the one Score judged. For a
	// single-turn case that is the only turn; for a follow-up case the earlier
	// ones are in Turns.
	Reply     string           `json:"reply"`
	ToolCalls []ToolInvocation `json:"tool_calls,omitempty"`

	// Turns is every turn the case ran, in order, scored or not. Always at
	// least one. It is what makes a follow-up failure diagnosable: "the agent
	// re-ran get_schema" means nothing without the turn that already ran it.
	Turns []TurnResult `json:"turns,omitempty"`

	DurationMS int64   `json:"duration_ms"`
	TokensIn   int64   `json:"tokens_in"`
	TokensOut  int64   `json:"tokens_out"`
	CostUSD    float64 `json:"cost_usd"`
	LLMCalls   int64   `json:"llm_calls"`
}

// TurnResult is one turn of a case. A single-turn case has exactly one and it
// duplicates the Result's own Reply/ToolCalls, which is the cheaper trade: the
// alternative is a report reader having to know which cases are multi-turn
// before they can find the reply.
type TurnResult struct {
	Question   string           `json:"question"`
	Reply      string           `json:"reply"`
	ToolCalls  []ToolInvocation `json:"tool_calls,omitempty"`
	DurationMS int64            `json:"duration_ms"`
	// Error is set when this turn failed to run at all, which abandons the
	// case — there is no honest way to ask a follow-up to an answer that never
	// arrived.
	Error string `json:"error,omitempty"`
}

// Report is a whole run.
type Report struct {
	Set string `json:"set"`
	// Model is what was asked for. Served is what answered — see T-Q15 and
	// llmusage.Serving: the two are the same string on a good day and the
	// difference between a regression and provider drift on a bad one.
	Model     string        `json:"model"`
	Served    []ServedModel `json:"served,omitempty"`
	Declared  []string      `json:"declared_models,omitempty"`
	StartedAt time.Time     `json:"started_at"`
	Duration  string        `json:"duration"`

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

// ServedModel is one identity the provider reported while this run was scored,
// and how many responses it answered.
//
// A run normally has exactly one. Two rows mean the gateway re-routed
// mid-set, which is the thing T-Q15 exists to make visible: it is not
// detectable from a pass rate, and it is the leading explanation for a score
// that moves with no commit behind it.
type ServedModel struct {
	Model     string `json:"model"`
	Provider  string `json:"provider,omitempty"`
	Responses int    `json:"responses"`
}

// String renders the row a coverage doc pastes.
func (s ServedModel) String() string {
	out := s.Model
	if out == "" {
		out = "(unnamed)"
	}
	if s.Provider != "" {
		out += " via " + s.Provider
	}
	return fmt.Sprintf("%s (%d responses)", out, s.Responses)
}

// ServedFrom converts what the HTTP tap observed into the report's own shape.
// Nil when the provider named nothing, which prints as an explicit "not
// reported" rather than as a blank line — an absent identity is a fact about
// the gateway and should read like one.
func ServedFrom(observed []llmusage.ObservedServing) []ServedModel {
	if len(observed) == 0 {
		return nil
	}
	out := make([]ServedModel, 0, len(observed))
	for _, o := range observed {
		out = append(out, ServedModel{Model: o.Model, Provider: o.Provider, Responses: o.Responses})
	}
	return out
}

// SameServing reports whether two runs were answered by the same set of model
// identities. It is the question a re-score asks when the number moved: if
// this is false, the tree is not the only thing that changed.
func SameServing(a, b []ServedModel) bool {
	key := func(list []ServedModel) map[string]bool {
		out := map[string]bool{}
		for _, s := range list {
			out[s.Model+"|"+s.Provider] = true
		}
		return out
	}
	ka, kb := key(a), key(b)
	if len(ka) != len(kb) {
		return false
	}
	for k := range ka {
		if !kb[k] {
			return false
		}
	}
	return true
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
	b.WriteString(r.servedLines())
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
			// The turns before the scored one, so a follow-up failure can be
			// read. Skipped entirely for a single-turn case, where they would
			// only repeat the two lines below.
			if len(res.Turns) > 1 {
				for i, t := range res.Turns[:len(res.Turns)-1] {
					fmt.Fprintf(&b, "    turn %d: %s\n", i+1, truncate(oneLine(t.Question), 120))
					fmt.Fprintf(&b, "      tools: %s\n", describeCalls(t.ToolCalls))
					fmt.Fprintf(&b, "      reply: %s\n", truncate(oneLine(t.Reply), 200))
				}
				fmt.Fprintf(&b, "    turn %d (scored): %s\n", len(res.Turns),
					truncate(oneLine(res.Turns[len(res.Turns)-1].Question), 120))
			}
			fmt.Fprintf(&b, "    tools: %s\n", describeCalls(res.ToolCalls))
			fmt.Fprintf(&b, "    reply: %s\n", truncate(oneLine(res.Reply), 300))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// servedLines renders the identity block that follows the model line. Every
// branch prints something: the number this report carries is only re-runnable
// if a reader can tell which of the three cases they are in.
func (r Report) servedLines() string {
	var b strings.Builder
	switch len(r.Served) {
	case 0:
		b.WriteString("served:     not reported by the provider — this number names no revision\n")
	case 1:
		fmt.Fprintf(&b, "served:     %s\n", r.Served[0])
	default:
		b.WriteString("served:     ! more than one identity answered this run\n")
		for _, s := range r.Served {
			fmt.Fprintf(&b, "              %s\n", s)
		}
		b.WriteString("            The gateway re-routed mid-set. A score computed across two\n" +
			"            routes is two measurements added together — re-run before reading it.\n")
	}
	if len(r.Declared) > 0 {
		declared := false
		for _, m := range r.Declared {
			if strings.EqualFold(strings.TrimSpace(m), strings.TrimSpace(r.Model)) {
				declared = true
				break
			}
		}
		if !declared {
			fmt.Fprintf(&b, "            ! %s is not one of the models this set declares (%s)\n",
				r.Model, strings.Join(r.Declared, ", "))
		}
	}
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

// Matrix is one run of the same set across several models (T-Q5).
//
// It exists because every number this project has ever published about agent
// quality was measured on `deepseek/deepseek-v3.2` and nothing else. The
// baseline says so on its own headline line, and a pass rate on one model is a
// fact about that model as much as about the prompt — which makes "did this
// prompt change help?" and "would a different model help more?" the same
// unanswered question.
//
// Reports are in the order the models were given, which is the order they ran.
type Matrix struct {
	Set     string   `json:"set"`
	Models  []string `json:"models"`
	Reports []Report `json:"reports"`
}

// Disagreement is one case the models did not answer equally well. It is the
// most useful row this comparison produces: an aggregate pass rate says one
// model is better, and this says at what.
type Disagreement struct {
	ID       string            `json:"id"`
	Category string            `json:"category"`
	PassedOn []string          `json:"passed_on"`
	FailedOn []string          `json:"failed_on"`
	Failures map[string]string `json:"failures,omitempty"`
}

// Disagreements returns the cases whose outcome differed between models,
// ordered as the set was. A case every model failed is NOT a disagreement — it
// is a property of the set or the prompt, which is a different finding and one
// the per-model failure lists already carry.
func (m Matrix) Disagreements() []Disagreement {
	if len(m.Reports) < 2 {
		return nil
	}
	// Ordered by the first report, so the output reads in set order rather
	// than in map order.
	var out []Disagreement
	for _, base := range m.Reports[0].Results {
		d := Disagreement{ID: base.ID, Category: base.Category, Failures: map[string]string{}}
		for i, rep := range m.Reports {
			model := m.Models[i]
			var found *Result
			for j := range rep.Results {
				if rep.Results[j].ID == base.ID {
					found = &rep.Results[j]
					break
				}
			}
			if found == nil {
				continue
			}
			if found.Passed {
				d.PassedOn = append(d.PassedOn, model)
			} else {
				d.FailedOn = append(d.FailedOn, model)
				if len(found.Failures) > 0 {
					d.Failures[model] = found.Failures[0]
				}
			}
		}
		if len(d.PassedOn) > 0 && len(d.FailedOn) > 0 {
			out = append(out, d)
		}
	}
	return out
}

// Text renders the comparison a human pastes into a ticket gate.
func (m Matrix) Text() string {
	var b strings.Builder

	fmt.Fprintf(&b, "\n=== Argentum eval matrix — %s ===\n", m.Set)
	fmt.Fprintf(&b, "models: %s\n\n", strings.Join(m.Models, ", "))

	fmt.Fprintf(&b, "%-34s %8s %10s %10s %12s\n", "model", "pass", "mean lat", "mean cost", "total cost")
	for i, rep := range m.Reports {
		fmt.Fprintf(&b, "%-34s %7.1f%% %9.0fms %10.6f %12.6f\n",
			truncate(m.Models[i], 34), rep.PassRat*100,
			rep.MeanLatencyMS, rep.MeanCostUSD, rep.TotalCostUSD)
	}

	// What actually answered each column. A matrix compares models, so a
	// column whose identity is unknown compares nothing (T-Q15).
	b.WriteString("\n--- served by ---\n")
	for i, rep := range m.Reports {
		switch len(rep.Served) {
		case 0:
			fmt.Fprintf(&b, "  %-32s not reported by the provider\n", truncate(m.Models[i], 32))
		default:
			parts := make([]string, 0, len(rep.Served))
			for _, s := range rep.Served {
				parts = append(parts, s.String())
			}
			fmt.Fprintf(&b, "  %-32s %s\n", truncate(m.Models[i], 32), strings.Join(parts, "; "))
		}
	}

	// Per category, side by side. An aggregate that moves two points can hide
	// a category that collapsed, which is the whole argument for CategoryScore
	// existing in the first place.
	cats := map[string]bool{}
	for _, rep := range m.Reports {
		for c := range rep.ByCategory {
			cats[c] = true
		}
	}
	names := make([]string, 0, len(cats))
	for c := range cats {
		names = append(names, c)
	}
	sort.Strings(names)

	b.WriteString("\n--- by category ---\n")
	fmt.Fprintf(&b, "%-20s", "")
	for _, model := range m.Models {
		fmt.Fprintf(&b, " %14s", truncate(model, 14))
	}
	b.WriteString("\n")
	for _, cat := range names {
		fmt.Fprintf(&b, "%-20s", cat)
		for _, rep := range m.Reports {
			cs, ok := rep.ByCategory[cat]
			if !ok {
				fmt.Fprintf(&b, " %14s", "—")
				continue
			}
			fmt.Fprintf(&b, " %13.0f%%", cs.PassRate*100)
		}
		b.WriteString("\n")
	}

	dis := m.Disagreements()
	if len(dis) == 0 {
		b.WriteString("\nNo case changed outcome between models. A difference in pass rate would\n" +
			"then be arithmetic rather than behaviour — check the per-model failures.\n")
		return b.String()
	}

	b.WriteString("\n--- cases the models disagree on ---\n")
	b.WriteString("The rows worth reading. An aggregate says one model is better; these say at what.\n")
	for _, d := range dis {
		fmt.Fprintf(&b, "\n  %s [%s]\n", d.ID, d.Category)
		fmt.Fprintf(&b, "    passed: %s\n", strings.Join(d.PassedOn, ", "))
		fmt.Fprintf(&b, "    failed: %s\n", strings.Join(d.FailedOn, ", "))
		for _, model := range d.FailedOn {
			if why := d.Failures[model]; why != "" {
				fmt.Fprintf(&b, "      %s: %s\n", truncate(model, 24), truncate(why, 160))
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}
