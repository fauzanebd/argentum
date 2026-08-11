package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func argsOf(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	m := map[string]interface{}{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("bad test fixture %q: %v", raw, err)
		}
	}
	return m
}

// The SQL is the single most valuable field carried forward: a follow-up that
// can read the previous SELECT extends it instead of rebuilding it.
func TestBuildToolDigestCarriesTheQueryAndItsShape(t *testing.T) {
	d := BuildToolDigest("run_sql",
		argsOf(t, `{"source_id":"src-1","query":"SELECT channel, SUM(sales_amount) FROM fact_sales GROUP BY 1"}`),
		argsOf(t, `{"row_count":3,"columns":["channel","sum"]}`),
	)
	if d.Tool != "run_sql" || d.SourceID != "src-1" {
		t.Errorf("tool/source lost: %+v", d)
	}
	if !strings.Contains(d.Query, "fact_sales") {
		t.Errorf("query lost: %q", d.Query)
	}
	if d.Rows != 3 {
		t.Errorf("rows = %d, want 3", d.Rows)
	}
	if len(d.Columns) != 2 {
		t.Errorf("columns = %v, want 2", d.Columns)
	}
}

// The tools do not agree on a name for the row count, and the audit path has
// the same problem. Reading both spellings beats changing a tool's output
// shape to suit a digest.
func TestBuildToolDigestReadsEitherRowCountSpelling(t *testing.T) {
	for _, body := range []string{`{"row_count":7}`, `{"rows_returned":7}`, `{"count":7}`} {
		if d := BuildToolDigest("run_sql", nil, argsOf(t, body)); d.Rows != 7 {
			t.Errorf("%s: rows = %d, want 7", body, d.Rows)
		}
	}
	// A result carrying the rows themselves rather than a count.
	if d := BuildToolDigest("run_sql", nil, argsOf(t, `{"rows":[{"a":1},{"a":2}]}`)); d.Rows != 2 {
		t.Errorf("counted %d rows from the array, want 2", d.Rows)
	}
	// No count anywhere stays -1, which RenderPriorWork reads as "do not claim
	// a number" — distinct from a genuine zero.
	if d := BuildToolDigest("get_schema", nil, argsOf(t, `{}`)); d.Rows != -1 {
		t.Errorf("absent row count = %d, want -1", d.Rows)
	}
}

// A failed query carried forward is what stops the next turn repeating it —
// the second most expensive thing this agent does.
func TestBuildToolDigestCarriesFailures(t *testing.T) {
	d := BuildToolDigest("run_sql",
		argsOf(t, `{"query":"SELECT * FROM sales"}`),
		argsOf(t, `{"error":"relation \"sales\" does not exist"}`),
	)
	if d.Err == "" {
		t.Fatal("the error was dropped")
	}
	if !strings.Contains(RenderPriorWork([]ToolDigest{d}), "FAILED") {
		t.Error("a failed call does not read as failed in the context block")
	}
}

// get_schema answers with objects, not strings. A digest that only understood
// strings would record that the schema was read and not what it found, which
// is the half that saves the next turn a round trip.
func TestBuildToolDigestReadsTableObjects(t *testing.T) {
	d := BuildToolDigest("get_schema", nil,
		argsOf(t, `{"tables":[{"name":"fact_sales"},{"name":"dim_date"},"dim_products"]}`))
	got := strings.Join(d.Tables, ",")
	for _, want := range []string{"fact_sales", "dim_date", "dim_products"} {
		if !strings.Contains(got, want) {
			t.Errorf("table %q missing from %q", want, got)
		}
	}
}

// Sorted output, so re-running a turn produces a byte-identical digest. Two
// digests differing only by map iteration order would read as a change.
func TestDigestNamesAreStable(t *testing.T) {
	body := `{"tables":["z_table","a_table","m_table"]}`
	first := BuildToolDigest("get_schema", nil, argsOf(t, body))
	second := BuildToolDigest("get_schema", nil, argsOf(t, body))
	if strings.Join(first.Tables, ",") != strings.Join(second.Tables, ",") {
		t.Error("two digests of the same result disagree")
	}
	if first.Tables[0] != "a_table" {
		t.Errorf("names are not sorted: %v", first.Tables)
	}
}

func TestDigestQueryIsCapped(t *testing.T) {
	long := "SELECT " + strings.Repeat("column_name, ", 200) + "1"
	d := BuildToolDigest("run_sql", map[string]interface{}{"query": long}, nil)
	if !strings.HasPrefix(d.Query, "SELECT column_name") {
		t.Errorf("the head of the query was not kept: %q", d.Query)
	}
	// The cap plus the " …" marker, which is four bytes rather than two.
	if len(d.Query) > digestMaxQueryChars+len(" …") {
		t.Errorf("query kept %d chars, cap is %d", len(d.Query), digestMaxQueryChars)
	}
	if !strings.HasSuffix(d.Query, "…") {
		t.Error("a truncated query does not say it was truncated")
	}
}

// A turn calls get_schema on the same source twice more often than it should.
// Carrying both forward spends the next turn's context saying one thing twice.
func TestDedupeCollapsesRepeatsAndKeepsDistinctQueries(t *testing.T) {
	in := []ToolDigest{
		{Tool: "get_schema", SourceID: "src-1"},
		{Tool: "get_schema", SourceID: "src-1"},
		{Tool: "run_sql", SourceID: "src-1", Query: "SELECT 1"},
		{Tool: "run_sql", SourceID: "src-1", Query: "SELECT 2"},
		{Tool: "get_schema", SourceID: "src-2"},
	}
	got := DedupeDigests(in)
	if len(got) != 4 {
		t.Fatalf("deduped to %d, want 4: %+v", len(got), got)
	}
	if got[0].Tool != "get_schema" || got[0].SourceID != "src-1" {
		t.Errorf("order not preserved: %+v", got)
	}
}

func TestDedupeBoundsTheList(t *testing.T) {
	in := make([]ToolDigest, 0, 40)
	for i := 0; i < 40; i++ {
		in = append(in, ToolDigest{Tool: "run_sql", Query: strings.Repeat("x", i+1)})
	}
	if got := DedupeDigests(in); len(got) != maxDigestsPerTurn {
		t.Errorf("kept %d digests, want the cap %d", len(got), maxDigestsPerTurn)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []ToolDigest{{Tool: "run_sql", SourceID: "s", Query: "SELECT 1", Rows: 4, Columns: []string{"a"}}}
	got := DecodeDigests(EncodeDigests(in))
	if len(got) != 1 || got[0].Query != "SELECT 1" || got[0].Rows != 4 {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// A malformed row must not be able to stop the next turn from running: the
// turn it belongs to is over, and its memory is a convenience.
func TestDecodeDigestsSurvivesGarbage(t *testing.T) {
	for _, raw := range []string{"", "   ", "not json", "{}", "[1,2,3]"} {
		if got := DecodeDigests(raw); len(got) > 0 && got[0].Tool != "" {
			t.Errorf("DecodeDigests(%q) invented %+v", raw, got)
		}
	}
}

func TestRenderPriorWorkIsEmptyWithNothingToSay(t *testing.T) {
	if got := RenderPriorWork(nil); got != "" {
		t.Errorf("rendered %q for an empty history", got)
	}
}

// The block must tell the agent to reuse the work AND not to quote a stale
// figure as if it were fresh. Without the second half this feature would be a
// new way to state a number no tool produced this turn — the exact failure
// CheckFabrication exists for.
func TestRenderPriorWorkForbidsQuotingStaleFigures(t *testing.T) {
	got := RenderPriorWork([]ToolDigest{{Tool: "run_sql", Query: "SELECT SUM(x) FROM y", Rows: 1}})
	if !strings.Contains(got, "do not re-read a schema") {
		t.Error("the block does not tell the agent to reuse its earlier work")
	}
	if !strings.Contains(got, "re-run the query rather than quoting a number") {
		t.Error("the block does not stop a stale figure being quoted as fresh")
	}
	if !strings.Contains(got, "SELECT SUM(x) FROM y") {
		t.Error("the previous query is not in the block")
	}
}
