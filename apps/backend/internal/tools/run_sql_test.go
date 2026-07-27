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
