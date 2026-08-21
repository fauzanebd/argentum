package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// These read the repository's own source. That is unusual and it is the only
// honest option here: the property under test is "no statement in this file can
// delete an audit row", and a fake database would answer with whatever the fake
// was written to answer. Proving it against a real Postgres is the live gate;
// proving the statements never *name* the table is something `go test` can do
// in a millisecond and would have caught the mistake this guards against.
//
// The mistake is specific. `agent_actions` survives an erasure because
// migration 023 gave it no foreign key on `thread_id` — a decision made three
// tickets ago, recorded in a comment, and invisible from inside this file. The
// way it gets lost is somebody adding a tidy-up statement here, not somebody
// editing 023.

func retentionSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("retention_repo.go")
	if err != nil {
		t.Fatalf("read retention_repo.go: %v", err)
	}
	return string(raw)
}

// The tables an erasure must not touch, and why each one is on the list.
var protectedFromErasure = map[string]string{
	"agent_actions": "the audit log — what the agent did, under whose authority. " +
		"Migration 023 deliberately gave it no thread FK so a thread delete could not launder it",
	"usage_events": "what the tenant was billed. Not personal data, and the one record a billing dispute needs",
	"api_request_stats": "the integrator's own request history (T-A5), " +
		"which apiobs prunes on its own 30-day window",
	"data_erasures": "the record of the erasure itself. A route that erases this " +
		"answers a regulator's question with silence",
}

func TestRetentionStatementsNeverNameTheProtectedTables(t *testing.T) {
	src := retentionSource(t)
	// Comments legitimately discuss these tables at length — the file argues
	// about agent_actions in a doc comment — so the check runs over code only.
	code := stripComments(src)

	for table, why := range protectedFromErasure {
		if strings.Contains(code, table) {
			t.Errorf("retention_repo.go names %q in a statement.\nIt must not: %s", table, why)
		}
	}
}

// Every DELETE in the file must carry a company predicate. `messages` has no
// company_id of its own, so the tenant boundary lives in a join or a subquery —
// which is exactly the kind of predicate that can be dropped during a refactor
// without the statement looking wrong. A `DELETE FROM messages` with no tenant
// clause empties the table for every customer at once.
func TestEveryRetentionDeleteIsTenantScoped(t *testing.T) {
	code := stripComments(retentionSource(t))

	// Statements are const strings; splitting on the SQL keyword is enough to
	// isolate each one because nothing in this file builds SQL dynamically —
	// which is itself the property the next test pins.
	deletes := regexp.MustCompile(`(?is)DELETE\s+FROM.*?`).FindAllStringIndex(code, -1)
	if len(deletes) == 0 {
		t.Fatal("no DELETE statements found; this test is no longer reading what it thinks it is")
	}
	for _, loc := range deletes {
		// The statement runs to the closing backtick of its const.
		end := strings.Index(code[loc[0]:], "`")
		if end < 0 {
			end = len(code) - loc[0]
		}
		stmt := code[loc[0] : loc[0]+end]
		if !strings.Contains(stmt, "company_id") {
			t.Errorf("a DELETE has no company_id predicate:\n%s", strings.TrimSpace(stmt))
		}
	}
}

// No statement in this file may be assembled from a variable. A tenant
// predicate that is interpolated rather than bound is one that can be an empty
// string, and an empty string in a WHERE clause on a bulk delete is the whole
// table.
func TestRetentionStatementsAreConstant(t *testing.T) {
	code := stripComments(retentionSource(t))
	if regexp.MustCompile(`(?i)(DELETE\s+FROM|SELECT\s)[^` + "`" + `]*"\s*\+`).MatchString(code) {
		t.Error("a statement in retention_repo.go is concatenated from a variable; bind it instead")
	}
	if strings.Contains(code, "fmt.Sprintf(") {
		t.Error("retention_repo.go builds SQL with fmt.Sprintf; every predicate here must be a bound parameter")
	}
}

// stripComments removes // and /* */ comments so a check about statements is
// not answered by prose. Deliberately crude — it does not understand a comment
// marker inside a string literal — which is safe in the only direction that
// matters: it can remove too much and make a test miss something, never add
// text that makes one pass.
func stripComments(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
