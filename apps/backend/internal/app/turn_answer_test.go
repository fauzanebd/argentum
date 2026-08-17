package app

import (
	"strings"
	"testing"
)

func iterMeta(n int) map[string]interface{} {
	return map[string]interface{}{"iteration": n, "choice_index": 0}
}

// The transcript this ticket was written from (T-Q11), replayed as the event
// sequence that produced it: iteration 1 guesses, calls a tool, guesses again;
// iteration 2 answers from the result. The record must be the last sentence.
func TestAnswerKeepsOnlyThePostToolIteration(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(iterMeta(1), "There were 1,667 transactions in November 2024. ")
	a.Write(iterMeta(1), "There were 1,667 transactions in November 2024. ")
	a.Write(iterMeta(2), "There were 300 transactions in November 2024.")

	got := a.String()
	if strings.Contains(got, "1,667") {
		t.Errorf("the pre-tool guess survived into the record: %q", got)
	}
	if !strings.Contains(got, "300") {
		t.Errorf("the true figure was dropped: %q", got)
	}
	if a.Dropped() == 0 {
		t.Error("prose was narrowed and Dropped() reports nothing")
	}
}

// A turn with one iteration is what most turns are, and it must be
// byte-identical to the concatenation this replaced.
func TestAnswerWithOneIterationIsUnchanged(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(iterMeta(1), "Revenue in ")
	a.Write(iterMeta(1), "November was ")
	a.Write(iterMeta(1), "Rp 12.3M.")

	if got, want := a.String(), "Revenue in November was Rp 12.3M."; got != want {
		t.Errorf("answer = %q, want %q", got, want)
	}
	if a.Dropped() != 0 {
		t.Errorf("a single-iteration turn dropped %d chars", a.Dropped())
	}
}

// A provider that stamps no iteration number must behave exactly as it did
// before this ticket. Emptying a reply on a provider this was never measured
// against is the one outcome worse than the defect.
func TestAnswerWithoutIterationMetadataConcatenates(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(nil, "one ")
	a.Write(map[string]interface{}{"choice_index": 0}, "two ")
	a.Write(nil, "three")

	if got, want := a.String(), "one two three"; got != want {
		t.Errorf("answer = %q, want %q", got, want)
	}
	if a.Dropped() != 0 {
		t.Errorf("an untagged turn dropped %d chars", a.Dropped())
	}
}

// The SDK's synthesis call runs after the iteration budget is spent, carries no
// iteration number, and is by construction the last word. Filed under 0 it
// would lose to every tagged iteration above it, and the turn would store its
// own working notes instead of its answer.
func TestFinalSynthesisCallWins(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(iterMeta(1), "Let me check the schema. ")
	a.Write(iterMeta(8), "Still working. ")
	a.Write(map[string]interface{}{"final_call": true}, "Sales in Q4 2024 were Rp 12.3B.")

	if got := a.String(); got != "Sales in Q4 2024 were Rp 12.3B." {
		t.Errorf("answer = %q, want the synthesis call", got)
	}
}

// agent-sdk-go can withhold intermediate content and replay it *after* the
// last iteration's answer has already streamed. Choosing "the last thing that
// arrived" would store the narration; choosing the highest iteration does not.
func TestReplayedNarrationDoesNotBecomeTheAnswer(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(iterMeta(2), "The answer is 300.")
	// The replay: the same events the provider held back, arriving last.
	a.Write(iterMeta(1), "I'll look that up. ")

	if got := a.String(); got != "The answer is 300." {
		t.Errorf("answer = %q, want the last iteration's answer", got)
	}
}

// A blank last iteration is not an answer; the last iteration that *produced*
// one is. A turn where nothing produced one returns empty and reaches
// rescueEmptyReply exactly as it does today.
func TestBlankIterationsAreSkippedAndAnEmptyTurnStaysEmpty(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(iterMeta(1), "The total is Rp 4.2M.")
	a.Write(iterMeta(2), "   \n")
	if got := a.String(); !strings.Contains(got, "4.2M") {
		t.Errorf("answer = %q, want the last iteration with prose in it", got)
	}

	empty := newAnswerBuffer()
	empty.Write(iterMeta(1), "  ")
	empty.Write(iterMeta(2), "\n")
	if got := empty.String(); strings.TrimSpace(got) != "" {
		t.Errorf("an empty turn produced %q instead of reaching the rescue", got)
	}
}

// A guardrail refusal is the reply, not a part of one. Everything the model
// wrote before the rule fired has to go.
func TestReplaceDropsEverythingWritten(t *testing.T) {
	a := newAnswerBuffer()
	a.Write(iterMeta(1), "Here are the customer email addresses:")
	a.Replace("I cannot share contact details.")

	if got := a.String(); got != "I cannot share contact details." {
		t.Errorf("answer = %q, want only the refusal", got)
	}
	if a.Dropped() != 0 {
		t.Errorf("Replace left %d chars behind", a.Dropped())
	}
}
