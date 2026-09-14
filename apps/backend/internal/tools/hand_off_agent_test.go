package tools

import (
	"context"
	"strings"
	"testing"
)

// T-N7's tool half. As with nudge_agent, every rule about who may be handed what
// is app.NudgeService's and tested there; this proves the handing-over neither
// guesses nor drops anything — and that the model is never asked for the question.

type recordingHandOffer struct {
	agent, reason string
	calls         int
}

func (r *recordingHandOffer) HandOff(_ context.Context, agent, reason string) string {
	r.calls++
	r.agent, r.reason = agent, reason
	return `{"handed_off":true,"agent":"Finance"}`
}

func TestHandOffHandsBothArgumentsToTheServiceVerbatim(t *testing.T) {
	h := &recordingHandOffer{}
	got, err := NewHandOffAgentTool(h).Execute(nudgeCtx(),
		`{"agent":"Finance","reason":"Write-offs are booked in Finance's ledger."}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if h.calls != 1 || h.agent != "Finance" || h.reason != "Write-offs are booked in Finance's ledger." {
		t.Errorf("service saw %d call(s) with agent=%q reason=%q", h.calls, h.agent, h.reason)
	}
	if got != `{"handed_off":true,"agent":"Finance"}` {
		t.Errorf("the tool rewrote the service's result: %q", got)
	}
}

// The question is not a parameter, and must not become one. The colleague is
// sent the person's words from the turn's payload; a `question` argument would be
// a place for the model to paraphrase them, which is the retelling the ticket
// exists to remove.
func TestTheModelIsNeverAskedForTheQuestion(t *testing.T) {
	params := NewHandOffAgentTool(nil).Parameters()
	if len(params) != 2 {
		t.Errorf("parameters = %v, want exactly agent and reason", params)
	}
	for name := range params {
		if name != "agent" && name != "reason" {
			t.Errorf("hand_off_to_agent takes %q — the question travels from the payload, never from the model", name)
		}
	}
}

// The description is the one place the model reads the difference from
// nudge_agent, so it has to name that tool and give an example of each.
func TestTheDescriptionSaysWhenItIsANudgeInstead(t *testing.T) {
	d := NewHandOffAgentTool(nil).Description()
	if !strings.Contains(d, NudgeAgentName) || strings.Count(d, "For example") != 2 {
		t.Errorf("description does not set the hand-off against nudge_agent with one example each:\n%s", d)
	}
}

func TestAMalformedHandOffIsRefusedNotGuessed(t *testing.T) {
	h := &recordingHandOffer{}
	got, err := NewHandOffAgentTool(h).Execute(nudgeCtx(), `Finance: write-offs are theirs`)
	if err != nil {
		t.Fatalf("a formatting slip became a Go error: %v", err)
	}
	if h.calls != 0 {
		t.Errorf("the service was called %d time(s) on arguments that do not parse", h.calls)
	}
	if code := refusalCode(t, got); code != "malformed_arguments" {
		t.Errorf("refusal code = %q, want malformed_arguments", code)
	}
}

func TestAHandOffWithNoTenantIsAnError(t *testing.T) {
	h := &recordingHandOffer{}
	if _, err := NewHandOffAgentTool(h).Execute(context.Background(), `{"agent":"Finance","reason":"r"}`); err == nil {
		t.Fatal("a hand-off with no company on the context was not rejected")
	}
	if h.calls != 0 {
		t.Error("the service was reached with no tenant")
	}
}

func TestAHandOffWithNoServiceSaysSo(t *testing.T) {
	got, err := NewHandOffAgentTool(nil).Execute(nudgeCtx(), `{"agent":"Finance","reason":"r"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code := refusalCode(t, got); code != "not_available" {
		t.Errorf("refusal code = %q, want not_available", code)
	}
}
