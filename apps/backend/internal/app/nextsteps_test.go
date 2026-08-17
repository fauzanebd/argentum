package app

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

func allTools() []string {
	return []string{"list_sources", "get_schema", "list_metrics", "query_metric",
		"run_sql", "create_dashboard", "update_dashboard", "ask_clarification",
		"propose_action", "generate_document"}
}

func TestParseNextStepsReadsTheHappyShape(t *testing.T) {
	got := parseNextSteps(`{"steps":[
	  {"label":"Break down by region","prompt":"Break that down by region","recommended":true,"why":"the total hides where it came from"},
	  {"label":"Compare with last year","prompt":"How does that compare with last year?"}
	]}`)
	if len(got) != 2 {
		t.Fatalf("steps = %+v", got)
	}
	if !got[0].Recommended || got[1].Recommended {
		t.Errorf("recommended flags = %v, %v", got[0].Recommended, got[1].Recommended)
	}
}

// A model told "no markdown" writes a fence anyway. Answering that with no
// suggestions would be correct and would also mean the feature never fires on
// half the models this product runs on.
func TestParseNextStepsSurvivesAFenceAndAPreamble(t *testing.T) {
	got := parseNextSteps("Sure! Here you go:\n```json\n{\"steps\":[{\"label\":\"By region\",\"prompt\":\"By region\"}]}\n```")
	if len(got) != 1 || got[0].Label != "By region" {
		t.Fatalf("steps = %+v", got)
	}
}

// Every failure is the same outcome, and this is the one that would otherwise
// reach a user: unparseable text is no suggestions, not a panic and not a chip
// containing JSON.
func TestParseNextStepsOnRubbishIsEmpty(t *testing.T) {
	for _, in := range []string{"", "I don't know", "{", "[1,2,3]", "null"} {
		if got := parseNextSteps(in); len(got) != 0 {
			t.Errorf("parseNextSteps(%q) = %+v, want none", in, got)
		}
	}
}

// The model is not trusted with "at most one recommended". Keeping the first is
// the only choice that needs no opinion about which is better.
func TestNarrowKeepsOneRecommended(t *testing.T) {
	got := narrowSteps([]domain.NextStep{
		{Label: "A", Prompt: "a", Recommended: true, Why: "first"},
		{Label: "B", Prompt: "b", Recommended: true, Why: "second"},
	}, allTools())

	if len(got) != 2 {
		t.Fatalf("steps = %+v", got)
	}
	if !got[0].Recommended || got[1].Recommended {
		t.Errorf("recommended = %v, %v; want only the first", got[0].Recommended, got[1].Recommended)
	}
	// The reason renders on the recommended chip only, so carrying one on the
	// others is bytes on the wire and a tooltip nobody sees.
	if got[1].Why != "" {
		t.Errorf("why = %q on a non-recommended step", got[1].Why)
	}
}

func TestNarrowCapsAtThree(t *testing.T) {
	in := []domain.NextStep{
		{Label: "A", Prompt: "a"}, {Label: "B", Prompt: "b"},
		{Label: "C", Prompt: "c"}, {Label: "D", Prompt: "d"},
	}
	if got := narrowSteps(in, allTools()); len(got) != nextStepsMax {
		t.Errorf("steps = %d, want %d", len(got), nextStepsMax)
	}
}

// A suggestion is text this product puts in the user's mouth. It may name a
// dimension, a period or a metric; it may not assert a result.
func TestNarrowDropsAStepThatRestatesAFigure(t *testing.T) {
	got := narrowSteps([]domain.NextStep{
		{Label: "Why did revenue hit 3,863,405,700?", Prompt: "Why 3,863,405,700?"},
		{Label: "Break down by region", Prompt: "Break that down by region"},
	}, allTools())

	if len(got) != 1 || got[0].Label != "Break down by region" {
		t.Fatalf("steps = %+v, want only the figure-free one", got)
	}
}

// A year is a period, not a restated result. The rule has to leave the ordinary
// period language a business question is written in alone, or it deletes the
// suggestions a user is most likely to click.
func TestRestatesAFigureTellsPeriodsFromResults(t *testing.T) {
	periods := []string{
		"Compare that with 2024", "How did Q4 2025 finish?", "Show the last 30 days",
		"Top 10 customers", "Break that down by region", "Split by the top 5 channels",
	}
	for _, s := range periods {
		if restatesAFigure(s) {
			t.Errorf("restatesAFigure(%q) = true; that is a period, not a result", s)
		}
	}
	figures := []string{
		"Why did revenue hit 3,863,405,700?", "Explain the 12.5% drop",
		"Why is it Rp 66.215.000?", "What drove the 48291 orders?",
		"Is 1.234 right?",
	}
	for _, s := range figures {
		if !restatesAFigure(s) {
			t.Errorf("restatesAFigure(%q) = false; that restates a result", s)
		}
	}
}

// The narrowing that matters most: an agent scoped to two tools must not be
// made to offer work it cannot do. This is the inverse of the failure
// feature-coverage.md records — an agent told it held nine tools recommending
// work it could not perform.
func TestNarrowDropsStepsNeedingAToolTheAgentLacks(t *testing.T) {
	held := []string{"get_schema", "run_sql"}
	got := narrowSteps([]domain.NextStep{
		{Label: "Chart it", Prompt: "Show me that as a chart"},
		{Label: "Export to PDF", Prompt: "Export this to a PDF"},
		{Label: "Schedule it", Prompt: "Send me this every Monday"},
		{Label: "Split by channel", Prompt: "Split that by channel"},
	}, held)

	if len(got) != 1 || got[0].Label != "Split by channel" {
		t.Fatalf("steps = %+v, want only the one run_sql can answer", got)
	}
}

func TestNarrowKeepsChartStepsForAnAgentThatCanDrawThem(t *testing.T) {
	got := narrowSteps([]domain.NextStep{
		{Label: "Chart it", Prompt: "Show me that as a chart"},
	}, allTools())
	if len(got) != 1 {
		t.Fatalf("steps = %+v", got)
	}
}

func TestNarrowDropsIncompleteSteps(t *testing.T) {
	got := narrowSteps([]domain.NextStep{
		{Label: "", Prompt: "a"},
		{Label: "B", Prompt: "   "},
	}, allTools())
	if len(got) != 0 {
		t.Errorf("steps = %+v; a chip with no text or nothing to send is not a step", got)
	}
}

func TestNarrowTruncatesALongLabel(t *testing.T) {
	long := strings.Repeat("a", 120)
	got := narrowSteps([]domain.NextStep{{Label: long, Prompt: "go"}}, allTools())
	if len(got) != 1 {
		t.Fatalf("steps = %+v", got)
	}
	if n := len([]rune(got[0].Label)); n > nextStepsLabelMax {
		t.Errorf("label is %d runes, want ≤ %d", n, nextStepsLabelMax)
	}
	// The prompt is not truncated — it is what goes into the composer, and a
	// half-sentence there is a turn that asks the wrong question.
	if got[0].Prompt != "go" {
		t.Errorf("prompt = %q", got[0].Prompt)
	}
}

// The turns that get no chips, and why each one is worse than an empty space.
func TestSkipSuggestions(t *testing.T) {
	cases := []struct {
		name     string
		p        queue.ChatRunPayload
		answer   string
		tools    []string
		replaced bool
		skipped  bool
	}{
		{name: "ordinary dashboard turn", answer: "Revenue was up.", tools: []string{"run_sql"}},
		{
			name: "the turn asked a question", answer: "Which source did you mean?",
			tools: []string{"ask_clarification"}, skipped: true,
		},
		{
			name: "a guardrail wrote the reply", answer: "I could not complete that.",
			tools: []string{"run_sql"}, replaced: true, skipped: true,
		},
		{name: "empty answer", answer: "   ", skipped: true},
		{
			name: "report turn", p: queue.ChatRunPayload{Directive: "produce a PDF"},
			answer: "Here is your report.", skipped: true,
		},
		{
			name: "scheduled turn", p: queue.ChatRunPayload{ScheduledTaskID: "task-1"},
			answer: "The weekly numbers.", skipped: true,
		},
		{
			name: "watcher briefing", p: queue.ChatRunPayload{WatcherEventID: "ev-1"},
			answer: "Revenue breached.", skipped: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := skipSuggestions(tc.p, tc.answer, tc.tools, tc.replaced)
			if (reason != "") != tc.skipped {
				t.Errorf("reason = %q, skipped = %v", reason, tc.skipped)
			}
		})
	}
}

// A runner nobody configured behaves exactly as it did before this ticket. The
// pass costs money and latency; it does not switch itself on.
func TestSuggestNextStepsIsOffByDefault(t *testing.T) {
	r := &ChatRunner{}
	if got := r.suggestNextSteps(t.Context(), queue.ChatRunPayload{}, "an answer", allTools(), nil, false); got != nil {
		t.Errorf("steps = %+v on an unconfigured runner", got)
	}
}

// Nil steps write the column exactly as it was written before this ticket.
func TestNextStepsMetadataIsNilWhenThereAreNone(t *testing.T) {
	if got := nextStepsMetadata(nil); got != nil {
		t.Errorf("metadata = %v, want nil", got)
	}
	if got := nextStepsMetadata([]domain.NextStep{}); got != nil {
		t.Errorf("metadata = %v, want nil for an empty slice too", got)
	}
	got := nextStepsMetadata([]domain.NextStep{{Label: "A", Prompt: "a"}})
	if _, ok := got["next_steps"]; !ok {
		t.Errorf("metadata = %v, want a next_steps key", got)
	}
}

// The prompt is given the question, the answer and the capabilities — and no
// rows and no figures beyond whatever the answer itself already said.
func TestNextStepsPromptNamesTheCapabilities(t *testing.T) {
	got := nextStepsPrompt("what was revenue?", "Revenue was up.", []string{"run_sql"}, []string{"run_sql"})
	for _, want := range []string{"what was revenue?", "Revenue was up.", "run_sql", `{"steps":`} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt does not contain %q", want)
		}
	}
}
