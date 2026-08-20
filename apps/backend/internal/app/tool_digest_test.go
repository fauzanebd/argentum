package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/guardrails"
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
		argsOf(t, `{"row_count":3,"columns":["channel","sum"]}`), "",
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
		if d := BuildToolDigest("run_sql", nil, argsOf(t, body), ""); d.Rows != 7 {
			t.Errorf("%s: rows = %d, want 7", body, d.Rows)
		}
	}
	// A result carrying the rows themselves rather than a count.
	if d := BuildToolDigest("run_sql", nil, argsOf(t, `{"rows":[{"a":1},{"a":2}]}`), ""); d.Rows != 2 {
		t.Errorf("counted %d rows from the array, want 2", d.Rows)
	}
	// No count anywhere stays -1, which RenderPriorWork reads as "do not claim
	// a number" — distinct from a genuine zero.
	if d := BuildToolDigest("get_schema", nil, argsOf(t, `{}`), ""); d.Rows != -1 {
		t.Errorf("absent row count = %d, want -1", d.Rows)
	}
}

// A failed query carried forward is what stops the next turn repeating it —
// the second most expensive thing this agent does.
func TestBuildToolDigestCarriesFailures(t *testing.T) {
	d := BuildToolDigest("run_sql",
		argsOf(t, `{"query":"SELECT * FROM sales"}`),
		argsOf(t, `{"error":"relation \"sales\" does not exist"}`), "",
	)
	if d.Err == "" {
		t.Fatal("the error was dropped")
	}
	if !strings.Contains(RenderPriorWork([]ToolDigest{d}), "FAILED") {
		t.Error("a failed call does not read as failed in the context block")
	}
}

// The payload from the 2026-08-18 gate, verbatim (T-Q12). It carries no
// `error` key, because a refusal has to reach the model as a result it can act
// on rather than as a Go error it never sees — so every branch that reads
// failure off `error` recorded this as an ordinary call, and the next turn
// answered "Done. The dashboard has been updated" without calling anything.
func TestBuildToolDigestMarksABudgetRefusal(t *testing.T) {
	d := BuildToolDigest("update_dashboard",
		argsOf(t, `{"dashboard_id":"57f822e9"}`),
		argsOf(t, `{"budget_exhausted":true,"reason":"iteration budget spent (8 of 8)",`+
			`"retrieved_so_far":"9 tool call(s)","document_call_remaining":false,`+
			`"instruction":"Write your final reply now"}`), "")

	if d.Outcome() != DigestStatusRefused {
		t.Fatalf("outcome = %q, want %q — this is the whole of T-Q12", d.Outcome(), DigestStatusRefused)
	}
	if d.Ran() {
		t.Error("a refused call reports itself as having run")
	}
	if !strings.Contains(d.Err, "iteration budget spent") {
		t.Errorf("reason lost: %q", d.Err)
	}
	// The shape it used to have. Left as an assertion rather than a comment
	// because it is what the next turn read as success.
	if d.Rows != -1 {
		t.Errorf("rows = %d; a refused call retrieved nothing", d.Rows)
	}
}

// The sentence a model cannot read as success. A list of calls with no outcome
// beside them is an invitation to assume they worked.
func TestRenderPriorWorkSaysARefusedCallDidNotRun(t *testing.T) {
	got := RenderPriorWork([]ToolDigest{{
		Tool:   "update_dashboard",
		Status: DigestStatusRefused,
		Err:    "iteration budget spent (8 of 8)",
		Rows:   -1,
	}})
	if !strings.Contains(got, "REFUSED") || !strings.Contains(got, "did NOT run") {
		t.Errorf("a refused call does not read as refused:\n%s", got)
	}
	if !strings.Contains(got, "iteration budget spent") {
		t.Error("the reason is not in the block, so the turn cannot tell whether to retry")
	}
	if !strings.Contains(got, "never report it as done") {
		t.Error("the block does not forbid reporting refused work as done")
	}
}

// A tool that returns a Go error has no JSON result at all: agent-sdk-go
// renders it as a plain string, which parses to an empty map. Before T-Q12
// that produced a digest indistinguishable from a call that ran and said
// nothing — the same failure one door over, from the opposite cause.
func TestBuildToolDigestReadsAGoErrorFromTheRawResult(t *testing.T) {
	raw := "Error executing tool: dashboards are not configured on this deployment"
	d := BuildToolDigest("create_dashboard", argsOf(t, `{"title":"Q4"}`), nil, raw)
	if d.Outcome() != DigestStatusFailed {
		t.Fatalf("outcome = %q, want %q", d.Outcome(), DigestStatusFailed)
	}
	if !strings.Contains(d.Err, "dashboards are not configured") {
		t.Errorf("error text lost: %q", d.Err)
	}
	// The Anthropic path spells it differently and must be read too.
	if got := BuildToolDigest("run_sql", nil, nil, "Error: connection refused"); got.Outcome() != DigestStatusFailed {
		t.Errorf("anthropic-shaped error read as %q", got.Outcome())
	}
	// A tool answering in prose is not a failure. Calling it one would tell the
	// next turn its work is undone when it is not.
	if got := BuildToolDigest("ask_clarification", nil, nil, "Which region did you mean?"); got.Outcome() != DigestStatusOK {
		t.Errorf("a prose result read as %q", got.Outcome())
	}
}

// A successful call's stored digest is byte-identical to what it was before
// this field existed, so a thread written yesterday reads the same today.
func TestSuccessfulDigestCarriesNoStatusOnTheWire(t *testing.T) {
	d := BuildToolDigest("run_sql", argsOf(t, `{"query":"SELECT 1"}`), argsOf(t, `{"row_count":1}`), "")
	if d.Outcome() != DigestStatusOK {
		t.Fatalf("outcome = %q", d.Outcome())
	}
	if got := EncodeDigests([]ToolDigest{d}); strings.Contains(got, "status") {
		t.Errorf("a successful digest grew a field: %s", got)
	}
	// And a row written before Status existed still reads as failed on its Err
	// alone — the one direction that was never ambiguous.
	old := DecodeDigests(`[{"tool":"run_sql","rows":-1,"error":"syntax error"}]`)
	if len(old) != 1 || old[0].Outcome() != DigestStatusFailed {
		t.Errorf("a pre-T-Q12 failure reads as %+v", old)
	}
}

// A refusal and the retry that succeeded are two facts. Collapsing them keeps
// the first — the refusal — and the successful call would vanish.
func TestDedupeKeepsARefusalApartFromItsRetry(t *testing.T) {
	in := []ToolDigest{
		{Tool: "update_dashboard", Status: DigestStatusRefused, Err: "iteration budget spent", Rows: -1},
		{Tool: "update_dashboard", Rows: 0},
	}
	got := DedupeDigests(in)
	if len(got) != 2 {
		t.Fatalf("deduped to %d, want 2: %+v", len(got), got)
	}
	if got[1].Outcome() != DigestStatusOK {
		t.Errorf("the successful retry was collapsed away: %+v", got)
	}
}

// get_schema answers with objects, not strings. A digest that only understood
// strings would record that the schema was read and not what it found, which
// is the half that saves the next turn a round trip.
func TestBuildToolDigestReadsTableObjects(t *testing.T) {
	d := BuildToolDigest("get_schema", nil,
		argsOf(t, `{"tables":[{"name":"fact_sales"},{"name":"dim_date"},"dim_products"]}`), "")
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
	first := BuildToolDigest("get_schema", nil, argsOf(t, body), "")
	second := BuildToolDigest("get_schema", nil, argsOf(t, body), "")
	if strings.Join(first.Tables, ",") != strings.Join(second.Tables, ",") {
		t.Error("two digests of the same result disagree")
	}
	if first.Tables[0] != "a_table" {
		t.Errorf("names are not sorted: %v", first.Tables)
	}
}

func TestDigestQueryIsCapped(t *testing.T) {
	long := "SELECT " + strings.Repeat("column_name, ", 200) + "1"
	d := BuildToolDigest("run_sql", map[string]interface{}{"query": long}, nil, "")
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

// TestTheDigestSurvivesTheFence is the seam T-H8 could most easily have broken,
// and the failure would not have looked like a security change.
//
// Every tool result the model reads is now wrapped in an untrusted-content
// fence, applied by a decorator outside the audit one. A fenced string is not
// JSON. The runner unwraps it exactly once — `guardrails.Unfence` — and if that
// call were ever removed, the digest would parse a marker line, find no rows,
// and record every successful query as a call that returned nothing: the memory
// block would tell the next turn its work is undone, which is the T-Q12 defect
// arriving by a new route.
func TestTheDigestSurvivesTheFence(t *testing.T) {
	payload := `{"columns":["bulan","nilai"],"rows":[{"bulan":"Desember","nilai":3863405700}],"row_count":1}`
	args := map[string]interface{}{"source_id": "src-1", "query": "SELECT bulan, nilai FROM laporan"}

	fenced := guardrails.Fence("run_sql result", payload)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(guardrails.Unfence(fenced)), &parsed); err != nil {
		t.Fatalf("the unfenced payload is not JSON: %v", err)
	}
	got := BuildToolDigest("run_sql", args, parsed, guardrails.Unfence(fenced))

	if got.Outcome() != DigestStatusOK {
		t.Fatalf("digest outcome = %q, want ok", got.Outcome())
	}
	if got.Rows != 1 {
		t.Errorf("digest rows = %d, want 1", got.Rows)
	}

	// The control, and the reason the unwrap is a named call rather than an
	// inline expression somebody can drop: handed the fenced string, the digest
	// sees no rows at all.
	var unparsed map[string]interface{}
	_ = json.Unmarshal([]byte(fenced), &unparsed)
	blind := BuildToolDigest("run_sql", args, unparsed, fenced)
	if blind.Rows == 1 {
		t.Fatal("the fenced string parsed as JSON — this control no longer controls for anything")
	}
}

// The grounding evidence rides the same seam (T-Q9/T-Q11): the figures a reply
// is checked against are collected from the parsed result. Fenced and unparsed,
// the collection returns nothing and every figure in a correct answer reads as
// ungrounded — which is how a gate replaces a right answer with "I wasn't able
// to complete the query".
func TestGroundingEvidenceSurvivesTheFence(t *testing.T) {
	payload := `{"rows":[{"nilai":3863405700}],"row_count":1}`
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(guardrails.Unfence(guardrails.Fence("run_sql result", payload))), &parsed); err != nil {
		t.Fatalf("unfenced payload is not JSON: %v", err)
	}
	nums := guardrails.CollectNumbers(parsed, 10)
	found := false
	for _, n := range nums {
		if n == 3863405700 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the tool's own figure was not collected as evidence: %v", nums)
	}
}
