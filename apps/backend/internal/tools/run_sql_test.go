package tools

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// A zero-row result is where the agent invented "IDR 1,488,000" in the first
// eval run (T-16). The payload has to say what an empty set means, because
// the empty set on its own demonstrably was not enough of a signal.
func TestBuildSQLPayloadZeroRows(t *testing.T) {
	payload := buildSQLPayload("src-1", "postgres", &db.QueryResult{
		Columns: []string{"total"},
		Rows:    []map[string]interface{}{},
		Count:   0,
	})

	note, ok := payload["note"].(string)
	if !ok {
		t.Fatal("zero-row result carries no note")
	}
	if !strings.Contains(note, "ZERO rows") {
		t.Errorf("note = %q, want it to state the result matched nothing", note)
	}
	if !strings.Contains(strings.ToLower(note), "do not state a total") {
		t.Errorf("note = %q, want it to forbid stating a figure", note)
	}
	if payload["row_count"] != 0 {
		t.Errorf("row_count = %v, want 0", payload["row_count"])
	}
}

// Truncation is a different failure with different advice, and it was there
// first — the empty-set note must not overwrite it.
func TestBuildSQLPayloadTruncationNoteWins(t *testing.T) {
	payload := buildSQLPayload("src-1", "postgres", &db.QueryResult{
		Columns:   []string{"total"},
		Rows:      []map[string]interface{}{},
		Count:     0,
		Truncated: true,
	})
	note, _ := payload["note"].(string)
	if !strings.Contains(note, "truncated") {
		t.Errorf("note = %q, want the truncation note", note)
	}
}

func TestBuildSQLPayloadRowsCarryNoNote(t *testing.T) {
	payload := buildSQLPayload("src-1", "postgres", &db.QueryResult{
		Columns: []string{"total"},
		Rows:    []map[string]interface{}{{"total": 3863405700}},
		Count:   1,
	})
	if _, exists := payload["note"]; exists {
		t.Errorf("a successful result carries a note: %v", payload["note"])
	}
}

// An aggregate over no matching rows is one row of NULLs, not zero rows — and
// it is the shape of the C-1 question ("what were our total sales last month?")
// that this product was built to stop fabricating. Until 2026-08-11 the payload
// carried row_count 1, no note and no probe, so the model was handed
// `[{"total": null}]` and told nothing was wrong with it. Found by running the
// T-Q9 probe against the demo warehouse; every test here used a row-returning
// SELECT.
func TestBuildSQLPayloadNotesAnAggregateOverNoRows(t *testing.T) {
	payload := buildSQLPayload("src-1", "postgres", &db.QueryResult{
		Columns: []string{"total"},
		Rows:    []map[string]interface{}{{"total": nil}},
		Count:   1,
	})
	note, _ := payload["note"].(string)
	if !strings.Contains(note, "ZERO rows") {
		t.Errorf("note = %q, want the no-data note", note)
	}
}

// COUNT(*) over an empty set returns 0, not NULL. That is an honest answer and
// must never be reported as "nothing matched" — the distinction is the reason
// matchedNothing tests for NULL rather than for a falsy value.
func TestMatchedNothingAcceptsAZeroCount(t *testing.T) {
	res := &db.QueryResult{
		Columns: []string{"n"},
		Rows:    []map[string]interface{}{{"n": 0}},
		Count:   1,
	}
	if matchedNothing(res) {
		t.Error("a COUNT(*) of 0 was treated as no data; it is data")
	}
	if _, exists := buildSQLPayload("src-1", "postgres", res)["note"]; exists {
		t.Error("a COUNT(*) of 0 carried a no-data note")
	}
}

// A partially-NULL row is data. Only every-column-NULL is the empty-aggregate
// signature.
func TestMatchedNothingIgnoresAPartiallyNullRow(t *testing.T) {
	if matchedNothing(&db.QueryResult{
		Columns: []string{"total", "label"},
		Rows:    []map[string]interface{}{{"total": nil, "label": "Online"}},
		Count:   1,
	}) {
		t.Error("a row with one real value was treated as no data")
	}
}
