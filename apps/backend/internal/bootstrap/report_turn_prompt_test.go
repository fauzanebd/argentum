package bootstrap

import (
	"strings"
	"testing"
)

// The prompt a report turn runs under must not argue with itself.
//
// Measured, not theorised. The eval run of 2026-08-08 scored
// `report-directive-is-not-an-injection` as a failure on both models and in two
// different ways: `haiku-4.5` built a Metabase card and never called
// `generate_document`; `deepseek-v3.2` produced the file *and* called
// `create_visualization` three times. The case's question is "Total sales by
// month for the last six months, with a bar chart", and both models were
// obeying a real instruction — the shared prompt's chart rule — over the
// directive appended after it.
//
// Two contradicting rules in one prompt are resolved by the model rather than
// by us, and which one wins is a property of the weights. That is not something
// a stronger directive fixes; it is something the absence of the contradiction
// fixes.

// The contradiction, stated as a test: whatever the shared prompt says about
// charts and whatever the directive says about them cannot both be in force.
func TestAFileTurnIsNotToldToBuildAMetabaseCard(t *testing.T) {
	prompt := SystemPromptForTurn(PromptToolNames(), PromptTurn{FileDeliverable: true})
	whole := prompt + "\n\n" + reportDirective()

	// The directive's half. If this ever stops being true the test below is
	// asserting nothing, so it is checked rather than assumed.
	if !strings.Contains(reportDirective(), "Do not call create_dashboard") {
		t.Fatal("the report directive no longer forbids create_dashboard; this test is vacuous")
	}

	for _, forbidden := range []string{
		"call create_dashboard ONCE",
		"A CHART IS SOMETHING THE USER ASKS FOR",
	} {
		if strings.Contains(whole, forbidden) {
			t.Errorf("a file turn is told %q while its directive forbids exactly that;\n"+
				"the model decides which rule wins, and on 2026-08-08 both models decided wrong", forbidden)
		}
	}
}

// And the ordinary turn is untouched: a dashboard is still what a chart
// request produces when nobody asked for a file.
func TestAnOrdinaryTurnKeepsTheChartRules(t *testing.T) {
	prompt := SystemPromptForTurn(PromptToolNames(), PromptTurn{})
	for _, want := range []string{
		"call create_dashboard ONCE",
		"A CHART IS SOMETHING THE USER ASKS FOR",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("an ordinary turn lost %q; a chart request still produces a dashboard", want)
		}
	}
}

// A file turn keeps everything that is not about Metabase. The risk in a
// subtractive fix is subtracting too much — a report turn still has to know how
// to write SQL, which language to answer in, and what a document must contain.
func TestAFileTurnKeepsEverythingElse(t *testing.T) {
	prompt := SystemPromptForTurn(PromptToolNames(), PromptTurn{FileDeliverable: true})
	for _, want := range []string{
		"LANGUAGE IS THE TOP PRIORITY",
		"SQL DIALECT",
		"WHEN THE USER ASKS FOR A FILE, PRODUCE THE FILE",
		"NUMBER FORMATTING",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("a file turn lost %q", want)
		}
	}
	// The catalog is unchanged: create_dashboard is still a tool the turn holds,
	// and the directive is what says not to reach for it. Removing the tool
	// would be a different decision — and the wrong one, since a report turn
	// that legitimately needs a dashboard has no way back.
	if !strings.Contains(prompt, "create_dashboard: Build a live dashboard") {
		t.Error("the tool catalog lost create_dashboard; the guidelines were the target, not the tool")
	}
}

// The guidelines stay numbered without gaps when some are dropped, which is
// the invariant the whole composer is built around — nothing may refer to a
// guideline by number, and a prompt that skips from 4 to 6 invites somebody to.
func TestFileTurnGuidelinesAreStillNumberedWithoutGaps(t *testing.T) {
	prompt := SystemPromptForTurn(PromptToolNames(), PromptTurn{FileDeliverable: true})
	body := prompt[strings.Index(prompt, "CRITICAL GUIDELINES:"):]

	n := 0
	for _, line := range strings.Split(body, "\n") {
		if len(line) == 0 || line[0] < '1' || line[0] > '9' {
			continue
		}
		n++
		if !strings.HasPrefix(line, itoa(n)+". ") {
			t.Fatalf("guideline %d is numbered %q", n, firstWord(line))
		}
	}
	if n == 0 {
		t.Fatal("no guidelines rendered")
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}
