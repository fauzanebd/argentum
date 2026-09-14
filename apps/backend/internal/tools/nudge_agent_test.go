package tools

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// T-N6's tool half. The tool parses and hands over; every rule about who may ask
// whom is app.NudgeService's and tested there. What is left to prove here is
// that the handing-over neither guesses nor drops anything.

type recordingNudger struct {
	agent, question string
	calls           int
}

func (r *recordingNudger) Nudge(_ context.Context, agent, question string) string {
	r.calls++
	r.agent, r.question = agent, question
	return `{"asked":true,"agent":"Finance"}`
}

func nudgeCtx() context.Context { return tenantctx.WithCompanyID(context.Background(), "co-1") }

func refusalCode(t *testing.T, result string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("result is not JSON: %q", result)
	}
	code, _ := m["error"].(string)
	return code
}

func TestNudgeHandsBothArgumentsToTheServiceVerbatim(t *testing.T) {
	n := &recordingNudger{}
	got, err := NewNudgeAgentTool(n).Execute(nudgeCtx(),
		`{"agent":"Finance","question":"Was a goods-in posted for SKU 4471 after Monday?"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if n.calls != 1 || n.agent != "Finance" || n.question != "Was a goods-in posted for SKU 4471 after Monday?" {
		t.Errorf("service saw %d call(s) with agent=%q question=%q", n.calls, n.agent, n.question)
	}
	if got != `{"asked":true,"agent":"Finance"}` {
		t.Errorf("the tool rewrote the service's result: %q", got)
	}
}

// run_sql and ask_clarification fall back to the raw string. This tool must not:
// neither argument can be recovered from the other, and a guess is a question
// sent to the wrong colleague.
func TestAMalformedNudgeIsRefusedNotGuessed(t *testing.T) {
	n := &recordingNudger{}
	got, err := NewNudgeAgentTool(n).Execute(nudgeCtx(), `Finance: was a goods-in posted?`)
	if err != nil {
		t.Fatalf("a formatting slip became a Go error: %v", err)
	}
	if n.calls != 0 {
		t.Errorf("the service was called %d time(s) on arguments that do not parse", n.calls)
	}
	if code := refusalCode(t, got); code != "malformed_arguments" {
		t.Errorf("refusal code = %q, want malformed_arguments", code)
	}
}

func TestANudgeWithNoTenantIsAnError(t *testing.T) {
	n := &recordingNudger{}
	if _, err := NewNudgeAgentTool(n).Execute(context.Background(), `{"agent":"Finance","question":"q"}`); err == nil {
		t.Fatal("a nudge with no company on the context was not rejected")
	}
	if n.calls != 0 {
		t.Error("the service was reached with no tenant")
	}
}

// The API's name-only registry builds the tool with no service. Executed there
// it says so, as a result the model can read.
func TestANudgeWithNoServiceSaysSo(t *testing.T) {
	got, err := NewNudgeAgentTool(nil).Execute(nudgeCtx(), `{"agent":"Finance","question":"q"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code := refusalCode(t, got); code != "not_available" {
		t.Errorf("refusal code = %q, want not_available", code)
	}
}

// The registry's flag-gated tools, and the prompt-line test's reason to know
// about them: they are in AllNames, so each has a catalog line, and they are the
// only names GatedByFlag claims — a room's two, and nothing else (T-N6, T-N7).
func TestTheRoomToolsAreTheOnlyOnesTheAllowlistDoesNotDecide(t *testing.T) {
	var gated []string
	for _, name := range AllNames() {
		if GatedByFlag(name) {
			gated = append(gated, name)
		}
	}
	if want := []string{NudgeAgentName, HandOffAgentName}; !slices.Equal(gated, want) {
		t.Errorf("flag-gated tools = %v, want exactly %v", gated, want)
	}
}

// `error` is what keeps a refused call out of the ones that succeeded, so a
// reply saying "I asked Finance" after one is unevidenced (T-Q13).
func TestANudgeRefusalCarriesTheKeysItsReadersNeed(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(NudgeRefusal("not_in_conversation", "You can ask: Finance.")), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] != "not_in_conversation" || m["asked"] != false || m["row_count"] != float64(0) {
		t.Errorf("refusal = %v, want error, asked:false and row_count:0", m)
	}
}
