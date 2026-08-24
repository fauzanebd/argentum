package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/metric"
)

// A one-sided window is the all-time window restricted on one side, and the
// other side is the data's own boundary.
//
// This reverses `halfWindow`, which refused it. That refusal was written before
// there was evidence and the evidence arrived on 2026-08-23/24: deepseek sends
// {"metric_key":"revenue","to":"2024-12-31"}, is told in one result which bound
// is missing, which two shapes are legal, what December 2024 looks like written
// out, and not to repeat the call — and re-sends it byte-identical. Rewriting
// that message scored 0/3 and a prompt sentence scored 0/3 twice
// (docs/coverage/eval-q1.md §2). Up to eleven cases of the golden set were
// blocked by a syntactically valid intent being refused.
//
// The precedent is this tool's own: both bounds were required until
// 2026-08-14, when a question naming no window left the model three bad options
// and the answer was to make both-omitted legal. The comment beside it is the
// rule being applied again here — a guideline loses to a missing affordance.
func TestOneSidedWindowIsCompletedFromCoverage(t *testing.T) {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	all := metric.AllTimeWindow(now)

	t.Run("to without from starts at the beginning of the data", func(t *testing.T) {
		from, to, scope, err := resolveWindow("", "2024-12-31", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !from.Equal(all.From) {
			t.Errorf("from = %s, want the all-time start %s", from, all.From)
		}
		if got, want := to.Format("2006-01-02"), "2024-12-31"; got != want {
			t.Errorf("to = %s, want %s — the caller's bound must survive", got, want)
		}
		if scope != windowScopeOpenStart {
			t.Errorf("scope = %q, want %q", scope, windowScopeOpenStart)
		}
	})

	t.Run("from without to runs to the end of the data", func(t *testing.T) {
		from, to, scope, err := resolveWindow("2024-07-01", "", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := from.Format("2006-01-02"), "2024-07-01"; got != want {
			t.Errorf("from = %s, want %s", got, want)
		}
		if !to.Equal(all.To) {
			t.Errorf("to = %s, want the all-time end %s", to, all.To)
		}
		if scope != windowScopeOpenEnd {
			t.Errorf("scope = %q, want %q", scope, windowScopeOpenEnd)
		}
	})

	t.Run("both bounds are still the caller's own window", func(t *testing.T) {
		from, to, scope, err := resolveWindow("2024-12-01", "2024-12-31", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if from.Format("2006-01-02") != "2024-12-01" || to.Format("2006-01-02") != "2024-12-31" {
			t.Errorf("got %s..%s, want the caller's exact bounds", from, to)
		}
		if scope != windowScopeCaller {
			t.Errorf("scope = %q, want %q", scope, windowScopeCaller)
		}
	})

	t.Run("neither bound is all available data", func(t *testing.T) {
		from, to, scope, err := resolveWindow("", "", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !from.Equal(all.From) || !to.Equal(all.To) {
			t.Errorf("got %s..%s, want the all-time window", from, to)
		}
		if scope != windowScopeAll {
			t.Errorf("scope = %q, want %q", scope, windowScopeAll)
		}
	})

	t.Run("a malformed bound is still an error", func(t *testing.T) {
		if _, _, _, err := resolveWindow("", "not-a-date", now); err == nil {
			t.Error("a bound that cannot be parsed must not be silently completed")
		}
	})
}

// The model must not quote a boundary the caller did not choose as though they
// had — the failure the all-time note was written to prevent, which a one-sided
// window reintroduces on one side.
func TestOneSidedWindowTellsTheModelWhichBoundWasNotTheirs(t *testing.T) {
	for _, tc := range []struct {
		name, scope, mustSay string
	}{
		{"open start", windowScopeOpenStart, "earliest"},
		{"open end", windowScopeOpenEnd, "latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			annotateWindowScope(payload, tc.scope)
			note, _ := payload["window_note"].(string)
			if note == "" {
				t.Fatal("a completed bound must carry a note saying so")
			}
			if !strings.Contains(strings.ToLower(note), tc.mustSay) {
				t.Errorf("note %q should say which side was completed (%q)", note, tc.mustSay)
			}
			if payload["window_scope"] != tc.scope {
				t.Errorf("window_scope = %v, want %q", payload["window_scope"], tc.scope)
			}
			b, _ := json.Marshal(payload)
			if strings.Contains(string(b), "1900") {
				t.Error("the sentinel bound must not be quotable from the note")
			}
		})
	}
}
