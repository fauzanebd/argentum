package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// wideResult builds a result whose rows are large enough that the row cap is
// irrelevant and only the byte cap can save the context window — the exact
// shape the trimming loop exists for.
func wideResult(rows, cellLen int) *db.QueryResult {
	out := &db.QueryResult{Columns: []string{"id", "blob"}}
	for i := 0; i < rows; i++ {
		out.Rows = append(out.Rows, map[string]interface{}{
			"id":   i,
			"blob": strings.Repeat("x", cellLen),
		})
	}
	out.Count = len(out.Rows)
	return out
}

func decode(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal payload: %v\n%s", err, raw)
	}
	return m
}

func TestMarshalSQLResultUnderTheCapIsUntouched(t *testing.T) {
	res := wideResult(5, 10)
	out := marshalSQLResult("src-1", "postgres", res, 200_000, nil)

	m := decode(t, out)
	if got := m["row_count"]; got != float64(5) {
		t.Errorf("row_count = %v, want 5", got)
	}
	if got := m["truncated"]; got != false {
		t.Errorf("truncated = %v, want false", got)
	}
	if _, exists := m["note"]; exists {
		t.Errorf("an untrimmed result carries a note: %v", m["note"])
	}
	if res.Truncated {
		t.Error("the result was marked truncated without being trimmed")
	}
}

func TestMarshalSQLResultTrimsWideRowsToFit(t *testing.T) {
	// 50 rows × ~1 KB of payload each, capped at 8 KB: the row cap would let
	// all 50 through and blow the context; the byte cap has to drop most.
	res := wideResult(50, 1000)
	const cap = 8000

	out := marshalSQLResult("src-1", "postgres", res, cap, nil)
	if len(out) > cap {
		t.Fatalf("payload is %d bytes, want ≤ %d", len(out), cap)
	}

	m := decode(t, out)
	if m["truncated"] != true {
		t.Errorf("truncated = %v, want true", m["truncated"])
	}
	rows, ok := m["rows"].([]interface{})
	if !ok {
		t.Fatalf("rows is %T, want a list", m["rows"])
	}
	if len(rows) == 0 {
		t.Error("every row was dropped; want as many as fit")
	}
	if len(rows) >= 50 {
		t.Errorf("kept %d rows, want fewer than the 50 that did not fit", len(rows))
	}
	if got := m["row_count"]; got != float64(len(rows)) {
		t.Errorf("row_count = %v but %d rows are present — the count must describe what was sent", got, len(rows))
	}
	// The model has to be told the result is partial, or it reports a total
	// computed over a slice as if it were the whole answer.
	note, _ := m["note"].(string)
	if !strings.Contains(note, "truncated") {
		t.Errorf("note = %q, want the truncation note", note)
	}

	// Rows are dropped from the tail, so the first row survives intact.
	first, _ := rows[0].(map[string]interface{})
	if first["id"] != float64(0) {
		t.Errorf("first surviving row id = %v, want 0 — rows must be dropped from the tail", first["id"])
	}
}

func TestMarshalSQLResultKeepsAsManyRowsAsFit(t *testing.T) {
	// The loop must stop at the first size that fits, not keep going. Adding
	// one more row back has to exceed the cap.
	res := wideResult(40, 500)
	const cap = 6000

	marshalSQLResult("src-1", "postgres", res, cap, nil)
	kept := len(res.Rows)
	if kept == 0 || kept == 40 {
		t.Fatalf("kept %d of 40 rows; the cap did not bite as intended", kept)
	}

	oneMore := wideResult(kept+1, 500)
	oneMore.Truncated = true
	bigger, _ := json.Marshal(buildSQLPayload("src-1", "postgres", oneMore))
	if len(bigger) <= cap {
		t.Errorf("one more row is %d bytes, still under the %d cap — the loop trimmed further than it had to", len(bigger), cap)
	}
}

func TestMarshalSQLResultCapDisabled(t *testing.T) {
	// A non-positive cap disables trimming, per NewRunSQLTool's contract.
	for _, maxBytes := range []int{0, -1} {
		res := wideResult(20, 1000)
		out := marshalSQLResult("src-1", "postgres", res, maxBytes, nil)
		m := decode(t, out)
		if got := m["row_count"]; got != float64(20) {
			t.Errorf("maxBytes=%d: row_count = %v, want all 20 rows", maxBytes, got)
		}
		if m["truncated"] != false {
			t.Errorf("maxBytes=%d: truncated = %v, want false", maxBytes, m["truncated"])
		}
	}
}

func TestMarshalSQLResultSingleUnshrinkableRow(t *testing.T) {
	// One row larger than the whole cap. The loop drops it, ends with zero
	// rows, and must still produce valid JSON marked truncated — not the
	// zero-row "there is no figure here" note, which would be a different and
	// wrong statement about what happened.
	res := wideResult(1, 5000)
	const cap = 100

	out := marshalSQLResult("src-1", "postgres", res, cap, nil)
	m := decode(t, out)

	rows, _ := m["rows"].([]interface{})
	if len(rows) != 0 {
		t.Errorf("kept %d rows, want 0 — the single row cannot fit", len(rows))
	}
	if m["truncated"] != true {
		t.Errorf("truncated = %v, want true", m["truncated"])
	}
	note, _ := m["note"].(string)
	if !strings.Contains(note, "truncated") {
		t.Errorf("note = %q, want the truncation note, not the zero-row note", note)
	}
	if strings.Contains(note, "ZERO rows") {
		t.Error("a trimmed-to-nothing result claims the query matched nothing")
	}
}

func TestMarshalSQLResultZeroRowsKeepsItsOwnNote(t *testing.T) {
	// A genuinely empty result is not a truncated one, and the byte cap must
	// not turn it into one. This is the T-16 fabrication guard.
	res := &db.QueryResult{Columns: []string{"total"}, Rows: []map[string]interface{}{}}
	out := marshalSQLResult("src-1", "postgres", res, 10, nil)

	m := decode(t, out)
	if m["truncated"] != false {
		t.Errorf("truncated = %v, want false for a genuinely empty result", m["truncated"])
	}
	note, _ := m["note"].(string)
	if !strings.Contains(note, "ZERO rows") {
		t.Errorf("note = %q, want the zero-row note", note)
	}
}

func TestMarshalSQLResultAlwaysReturnsValidJSON(t *testing.T) {
	// Whatever the cap, the model receives a parseable payload — the tool
	// returns this string straight into the conversation.
	for _, maxBytes := range []int{-1, 0, 1, 50, 500, 5000, 50000} {
		res := wideResult(30, 400)
		out := marshalSQLResult("src-1", "postgres", res, maxBytes, nil)
		m := decode(t, out)
		if m["source_id"] != "src-1" {
			t.Errorf("maxBytes=%d: source_id = %v, want src-1", maxBytes, m["source_id"])
		}
		if m["db_type"] != "postgres" {
			t.Errorf("maxBytes=%d: db_type = %v, want postgres", maxBytes, m["db_type"])
		}
		if _, ok := m["columns"]; !ok {
			t.Errorf("maxBytes=%d: columns missing", maxBytes)
		}
	}
}
