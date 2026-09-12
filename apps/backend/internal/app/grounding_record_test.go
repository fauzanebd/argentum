package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
)

// A turn that was not measured writes the column exactly as it did before
// T-W3 — nil stays nil, which is also T-Q10's promise for a turn with no
// suggestions.
func TestAnUnmeasuredTurnWritesNoGroundingRecord(t *testing.T) {
	if got := withGroundingRecord(nil, "msg-1", nil); got != nil {
		t.Errorf("nil report: metadata = %v; want nil", got)
	}
	unchecked := guardrails.CheckGrounding("Revenue was 3,863,405,700.", nil)
	if got := withGroundingRecord(nil, "msg-1", &unchecked); got != nil {
		t.Errorf("unchecked report: metadata = %v; want nil — unmeasured is not clean", got)
	}
}

// The record has to survive the trip to JSONB in the shape the query reads:
// arrays, never null, and the turn id it joins on.
func TestAGroundingRecordMarshalsTheShapeTheQueryReads(t *testing.T) {
	rep := guardrails.CheckGrounding("Revenue was 3,863,405,700.", []float64{3863405700})
	meta := withGroundingRecord(nil, "msg-1", &rep)
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		`"turn":"msg-1"`,
		`"ungrounded":[]`,
		`"ungrounded_percents":[]`,
		`"stated":1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metadata %s does not contain %s", got, want)
		}
	}
}

// T-Q10's suggestions and this record share one column; neither may cost the
// other its key.
func TestAGroundingRecordKeepsTheNextSteps(t *testing.T) {
	rep := guardrails.CheckGrounding("Revenue was 3,863,405,700.", []float64{3863405700})
	meta := withGroundingRecord(nextStepsMetadata([]domain.NextStep{{Label: "A", Prompt: "a"}}), "msg-1", &rep)
	if _, ok := meta["next_steps"]; !ok {
		t.Errorf("metadata = %v; the suggestions were dropped", meta)
	}
	if _, ok := meta[groundingKey]; !ok {
		t.Errorf("metadata = %v; no grounding record", meta)
	}
}

// The two acceptance lines of T-W3, from the side that produces the facts the
// query reads: the same margin sentence is unaccounted on a turn that divided
// it in prose, and accounted for on a turn where compute returned it.
func TestTheMarginIsUnaccountedUntilComputeReturnsIt(t *testing.T) {
	const reply = "Gross margin last month was 18.42%."
	revenueAndCost := []float64{4182330000, 3411900000}

	divided := guardrails.CheckGrounding(reply, revenueAndCost)
	rec := withGroundingRecord(nil, "msg-1", &divided)[groundingKey].(map[string]any)
	if n := len(rec["ungrounded_percents"].([]float64)); n != 1 {
		t.Errorf("divided in the sentence: ungrounded percents = %d; want 1", n)
	}

	// compute's payload carries "computed": "0.1842", which CollectNumbers
	// parses into the turn's returned numbers.
	computed := guardrails.CheckGrounding(reply, append(revenueAndCost, 0.1842))
	rec = withGroundingRecord(nil, "msg-1", &computed)[groundingKey].(map[string]any)
	if n := len(rec["ungrounded_percents"].([]float64)); n != 0 {
		t.Errorf("returned by compute: ungrounded percents = %d; want 0", n)
	}
}
