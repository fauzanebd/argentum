package tools

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/taint"
)

// T-N5's audit line: "what did this turn read before it did that" has to answer
// "a message from the Finance agent, which had read invoice-4471.pdf". The row
// carries the kinds — and the boolean, which is the indexed column a review
// actually filters by; the turn's completion line carries the names.
func TestAuditRowRecordsWhatAPeerHandedTheTurn(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: `{"row_count":1}`}, rec)

	tr := taint.New()
	tr.Inherit(map[taint.Kind][]string{taint.KindDocument: {"invoice-4471.pdf"}})
	tr.Mark(taint.KindAgent, "Finance")

	if _, err := tool.Execute(taint.With(turnCtx(), tr), `{"sql":"SELECT 1"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	row := rec.only(t)
	if row.InputTaint != "agent,document" {
		t.Errorf("input_taint = %q, want \"agent,document\"", row.InputTaint)
	}
	if !row.DocumentTainted {
		t.Error("document_tainted = false on a call made after a peer handed over a document's content")
	}
}
