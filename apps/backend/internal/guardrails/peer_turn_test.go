package guardrails

import (
	"context"
	"testing"
)

// T-N6: a colleague's question skips the input topic classifier, and nothing
// else — against the shipped rules, because the rule that stands aside is
// named there and not here.

// A question that names no analytics vocabulary, so the topic rule's cheap
// regexes admit nothing and its classifier is what decides.
const peerQuestion = "Can you confirm that for me before I reply?"

func TestAColleaguesQuestionIsNotTopicClassified(t *testing.T) {
	person := &stubLLM{topic: "FALSE", injection: "FALSE"}
	if _, err := load(t, person).ProcessInput(context.Background(), peerQuestion); err == nil {
		t.Fatal("the fixture is wrong: a person's off-topic question was admitted, so this test proves nothing")
	}

	peer := &stubLLM{topic: "FALSE", injection: "FALSE"}
	got, err := load(t, peer).ProcessInput(WithPeerTurn(context.Background()), peerQuestion)
	if err != nil {
		t.Fatalf("a colleague's question was refused by the input rules: %v", err)
	}
	if got != peerQuestion {
		t.Errorf("input = %q, want it unchanged", got)
	}
	if peer.topicCalls != 0 {
		t.Errorf("the topic classifier was called %d time(s) on a colleague's question", peer.topicCalls)
	}
}

// The other half of the rule: a colleague's words are untrusted input
// (decision 5), and every rule about what the words contain still runs.
func TestAColleaguesQuestionStillMeetsTheInjectionRules(t *testing.T) {
	llm := &stubLLM{topic: "FALSE", injection: "TRUE"}
	if _, err := load(t, llm).ProcessInput(WithPeerTurn(context.Background()), peerQuestion); err == nil {
		t.Fatal("a colleague's question the injection classifier flagged was admitted")
	}
	if llm.injectionCalls == 0 {
		t.Error("the injection classifier was skipped on a colleague's question")
	}
}
