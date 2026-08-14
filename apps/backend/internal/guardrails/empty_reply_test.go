package guardrails

import (
	"strings"
	"testing"
)

// The specimen from the 2026-08-14 run: three tools, all successful, a
// dashboard actually built, and the empty string handed to the user.
func TestAnEmptyReplyAfterSuccessfulToolsBecomesASentence(t *testing.T) {
	ev := TurnEvidence{
		ToolCalls: 3,
		DataCalls: 1,
		DataRows:  12,
		Tools:     []string{"get_schema", "create_visualization", "create_dashboard"},
	}

	got, empty := CheckEmptyReply("", ev, "Show me the monthly revenue trend")
	if !empty {
		t.Fatal("an empty reply was passed through")
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("the replacement is itself empty")
	}
	for _, tool := range ev.Tools {
		if !strings.Contains(got, tool) {
			t.Errorf("the replacement does not name %s, which is the whole point of it:\n%s", tool, got)
		}
	}
}

// A reply that exists is not this guard's business, whatever the turn did.
func TestANonEmptyReplyIsUntouched(t *testing.T) {
	const reply = "Revenue in July was Rp 1.200.000."
	got, empty := CheckEmptyReply(reply, TurnEvidence{ToolCalls: 2, DataRows: 4}, "revenue in july?")
	if empty {
		t.Error("a reply with text was treated as empty")
	}
	if got != reply {
		t.Errorf("the reply was rewritten:\n  got:  %q\n  want: %q", got, reply)
	}
}

// Whitespace is a blank message to the user, so it is a blank message here.
// The ` ` is what a model emits often enough to be worth pinning.
func TestWhitespaceOnlyCountsAsEmpty(t *testing.T) {
	for _, reply := range []string{" ", "\n\n", "\t", " "} {
		if _, empty := CheckEmptyReply(reply, TurnEvidence{}, "hi"); !empty {
			t.Errorf("%q was not treated as an empty reply", reply)
		}
	}
}

// A turn that ran nothing gets a different sentence: there is no work to
// point at, and telling the user something they cannot see was produced would
// be the fabrication the guard beside this one exists to stop.
func TestATurnThatRanNothingSaysSo(t *testing.T) {
	got, empty := CheckEmptyReply("", TurnEvidence{}, "what was our best month?")
	if !empty {
		t.Fatal("an empty reply was passed through")
	}
	if strings.Contains(got, "still exists") {
		t.Errorf("a turn with no tool calls claimed it produced something:\n%s", got)
	}
	if !strings.Contains(got, "again") {
		t.Errorf("the message gives the user nothing to do next:\n%s", got)
	}
}

// The language rule incompleteAnswer follows: the guard that replaces a reply
// must not break the reply-language discipline the system prompt enforces.
func TestAnIndonesianQuestionGetsAnIndonesianMessage(t *testing.T) {
	got, _ := CheckEmptyReply("", TurnEvidence{ToolCalls: 1, Tools: []string{"run_sql"}},
		"Berapa total penjualan bulan lalu?")
	if !strings.Contains(got, "kesalahan di sisi kami") {
		t.Errorf("an Indonesian question got an English message:\n%s", got)
	}
	if !strings.Contains(got, "run_sql") {
		t.Errorf("the Indonesian message drops the tool names:\n%s", got)
	}
}

// A turn may call the same tool six times. The list says what kind of work
// happened; it is not a transcript.
func TestTheToolListIsDeduplicatedAndBounded(t *testing.T) {
	got, _ := CheckEmptyReply("", TurnEvidence{
		ToolCalls: 7,
		Tools: []string{
			"run_sql", "run_sql", "get_schema", "run_sql",
			"query_metric", "create_visualization", "create_dashboard",
		},
	}, "build me a dashboard")

	if n := strings.Count(got, "run_sql"); n != 1 {
		t.Errorf("run_sql named %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "and more") {
		t.Errorf("a seven-call turn was listed in full rather than trimmed:\n%s", got)
	}
	if strings.Contains(got, "create_dashboard") {
		t.Errorf("the list ran past its bound:\n%s", got)
	}
}

// Exactly the bound: four distinct tools are named, and nothing is trimmed,
// because there is nothing left to trim.
func TestFourToolsAreNamedWithoutTheTrimNotice(t *testing.T) {
	got, _ := CheckEmptyReply("", TurnEvidence{
		ToolCalls: 4,
		Tools:     []string{"get_schema", "run_sql", "create_visualization", "create_dashboard"},
	}, "build me a dashboard")

	if strings.Contains(got, "and more") {
		t.Errorf("four tools were reported as trimmed:\n%s", got)
	}
	if !strings.Contains(got, "create_dashboard") {
		t.Errorf("the fourth tool was dropped:\n%s", got)
	}
}
