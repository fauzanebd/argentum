package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/freshness"
)

type stubProber struct {
	rep   freshness.Report
	calls int
}

func (s *stubProber) For(context.Context, string, string) freshness.Report {
	s.calls++
	return s.rep
}

func payloadOf(t *testing.T, rep freshness.Report) map[string]interface{} {
	t.Helper()
	out := marshalSQLResult("src-1", "postgres", &db.QueryResult{
		Columns: []string{"total"},
		Rows:    []map[string]interface{}{{"total": 42}},
		Count:   1,
	}, 0, nil, nil, rep)
	var p map[string]interface{}
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return p
}

// The property that makes this safe to ship to every tenant at once: a source
// nobody configured produces exactly the payload run_sql produced before
// freshness existed.
func TestAnUnknownVerdictLeavesThePayloadUntouched(t *testing.T) {
	before := marshalSQLResult("src-1", "postgres", &db.QueryResult{
		Columns: []string{"total"},
		Rows:    []map[string]interface{}{{"total": 42}},
		Count:   1,
	}, 0, nil, nil, freshness.Report{})
	after := marshalSQLResult("src-1", "postgres", &db.QueryResult{
		Columns: []string{"total"},
		Rows:    []map[string]interface{}{{"total": 42}},
		Count:   1,
	}, 0, nil, nil, freshness.Report{Verdict: freshness.Unknown, Note: "should not appear"})

	if string(before) != string(after) {
		t.Fatalf("an unknown verdict changed the payload:\n before %s\n after  %s", before, after)
	}
	if got := payloadOf(t, freshness.Report{Verdict: freshness.Unknown}); got["data_freshness"] != nil {
		t.Errorf("data_freshness = %v; unknown attaches nothing", got["data_freshness"])
	}
}

func TestAStaleVerdictReachesTheModelWithADateAndGuidance(t *testing.T) {
	at := time.Date(2026, 9, 8, 2, 0, 0, 0, time.UTC)
	p := payloadOf(t, freshness.Report{
		Verdict: freshness.Stale, ObservedAt: at, Age: 72 * time.Hour,
		Note: "this source last loaded 3 days ago (2026-09-08 02:00 UTC)",
	})
	block, ok := p["data_freshness"].(map[string]interface{})
	if !ok {
		t.Fatalf("data_freshness = %#v; want a block", p["data_freshness"])
	}
	if block["verdict"] != "stale" {
		t.Errorf("verdict = %v; want stale", block["verdict"])
	}
	if block["data_as_of"] != "2026-09-08T02:00:00Z" {
		t.Errorf("data_as_of = %v", block["data_as_of"])
	}
	if block["guidance"] == nil {
		t.Error("a stale block reached the model with no guidance on what to do about it")
	}
	// The rows are still there. A stale answer is still the answer.
	if p["row_count"] != float64(1) {
		t.Errorf("row_count = %v; freshness must not change what was returned", p["row_count"])
	}
}

func TestAWarnVerdictIsADateNotAWarning(t *testing.T) {
	at := time.Date(2026, 9, 11, 6, 0, 0, 0, time.UTC)
	p := payloadOf(t, freshness.Report{
		Verdict: freshness.Warn, ObservedAt: at, Age: 6 * time.Hour,
		Note: "this source last loaded 6 hours ago (2026-09-11 06:00 UTC)",
	})
	block := p["data_freshness"].(map[string]interface{})
	if block["verdict"] != "warn" {
		t.Fatalf("verdict = %v; want warn", block["verdict"])
	}
	guidance, _ := block["guidance"].(string)
	if guidance == "" {
		t.Fatal("no guidance on a warn block")
	}
	// A warning at this level must not tell the model to hedge every figure;
	// that is the stale branch's job and over-applying it trains users to ignore
	// the caveat.
	if contains(guidance, "do not describe") {
		t.Errorf("warn guidance = %q; that is the stale branch's language", guidance)
	}
}

func TestAnUnconfiguredProberIsNeverAsked(t *testing.T) {
	if got := probeFreshness(context.Background(), nil, "co-1", "src-1"); got.Verdict != freshness.Unknown {
		t.Errorf("verdict = %q; want %q", got.Verdict, freshness.Unknown)
	}
	s := &stubProber{rep: freshness.Report{Verdict: freshness.Stale}}
	if got := probeFreshness(context.Background(), s, "", "src-1"); got.Verdict != freshness.Unknown {
		t.Errorf("verdict with no tenant = %q; want %q", got.Verdict, freshness.Unknown)
	}
	if s.calls != 0 {
		t.Errorf("calls = %d; a probe without a tenant must not be attempted", s.calls)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
