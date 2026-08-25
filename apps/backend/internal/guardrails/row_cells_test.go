package guardrails

import "testing"

// A figure inside a row's text cell is evidence, and it was invisible until
// 2026-08-25.
//
// Found by T-H11's adversarial gate: the agent listed support-ticket subjects
// and the turn logged `ungrounded=[88213]`, where 88213 is inside the row value
// "Refund not received for order 88213" that run_sql had just returned.
// CollectNumbers parses a string cell as a *whole* number, so digits embedded
// in text were never collected — and any reply quoting such a cell verbatim was
// recorded as stating a figure no tool returned.
//
// The fix is narrower than CollectNumbersInProse and wider than CollectNumbers:
// read embedded numbers from the cells of rows a data tool returned, and from
// nothing else. Table names, column names, SQL text and error messages stay
// unread, because each of those is a number that would ground a fabrication —
// which is the risk CollectNumbers' own comment guards and this must not undo.
func TestCollectNumbersReadsFiguresInsideRowCells(t *testing.T) {
	result := map[string]any{
		"columns": []any{"subject", "opened_at"},
		"rows": []any{
			map[string]any{"subject": "Refund not received for order 88213", "opened_at": "2024-12-03"},
			map[string]any{"subject": "Damaged packaging on delivery", "opened_at": "2024-12-04"},
		},
		"count": float64(2),
	}

	got := CollectNumbers(result, 50)
	if !contains(got, 88213) {
		t.Errorf("88213 was not collected from a row cell; got %v", got)
	}
	// The row count is a real returned number and stays collected.
	if !contains(got, 2) {
		t.Errorf("the count was lost; got %v", got)
	}
}

// The guard that keeps the widening honest: only rows are read this way.
func TestEmbeddedNumbersAreReadFromRowsAndNowhereElse(t *testing.T) {
	result := map[string]any{
		// A column named like a year, an error mentioning a line, and SQL with
		// literals in it. None of these is evidence for a figure in a reply.
		"columns": []any{"revenue_2019", "q4_target"},
		"error":   "syntax error at or near \"FROM\" at line 42",
		"sql":     "SELECT sum(x) FROM t WHERE region_id = 7 AND year = 1999",
		"rows":    []any{},
		"count":   float64(0),
	}

	got := CollectNumbers(result, 50)
	for _, forbidden := range []float64{2019, 4, 42, 7, 1999} {
		if contains(got, forbidden) {
			t.Errorf("%v was collected from outside a row cell; that grounds a fabrication (got %v)", forbidden, got)
		}
	}
}

// A result with no `rows` key must behave exactly as it did before — the metric
// tools return a flat object and their numbers are fields, not cells.
func TestFlatResultsAreUnchanged(t *testing.T) {
	result := map[string]any{
		"metric_key": "revenue",
		"value":      float64(1899065495),
		"row_count":  float64(1),
	}
	got := CollectNumbers(result, 50)
	if !contains(got, 1899065495) {
		t.Errorf("a metric value must still be collected; got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("collected %v, want exactly the two numeric fields", got)
	}
}

// The cap still holds, because the comparison downstream is quadratic in this
// slice and a wide result is thousands of numbers.
func TestEmbeddedCollectionRespectsTheCap(t *testing.T) {
	rows := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, map[string]any{"note": "order 1234 and 5678"})
	}
	got := CollectNumbers(map[string]any{"rows": rows}, 10)
	if len(got) > 10 {
		t.Errorf("collected %d numbers, want at most the cap of 10", len(got))
	}
}

func contains(xs []float64, want float64) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
