package sqlguard

import (
	"strings"
	"testing"
)

// The 2026-08-22 gate's finding, pinned.
//
// `T-H12` shipped a "table **and column** allowlist" whose column half refused
// exactly one thing: a `*`. A caller who knew the column name wrote it out and
// read straight through. This is that statement.
func TestANamedColumnOutsideTheAllowlistIsRefused(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(tbl string) bool { return tbl == "employees" }
	allowsColumn := func(tbl, col string) bool {
		if tbl != "employees" {
			return true
		}
		return col == "full_name" || col == "department"
	}

	// The repair the star refusal tells the model to make must still work.
	if err := ValidateReferences("SELECT full_name, department FROM employees", allows, restricted, allowsColumn); err != nil {
		t.Fatalf("allowed columns refused: %v", err)
	}

	err := ValidateReferences("SELECT salary FROM employees", allows, restricted, allowsColumn)
	if err == nil {
		t.Fatal("a column outside the allowlist was read by naming it — this is the gate's finding, unfixed")
	}
	if !strings.Contains(err.Error(), "salary") {
		t.Errorf("refusal does not name the column it refused: %v", err)
	}
	// The allowed set is not recited, for the same reason the table refusal
	// does not recite the table allowlist.
	if strings.Contains(err.Error(), "full_name") {
		t.Errorf("refusal lists what IS allowed: %v", err)
	}
}

// A column is not only read in a select list.
func TestAnExcludedColumnIsRefusedInEveryPosition(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(tbl string) bool { return tbl == "employees" }
	allowsColumn := func(_, col string) bool { return col == "full_name" || col == "dept" }

	for _, sql := range []string{
		"SELECT full_name FROM employees WHERE salary > 100",
		"SELECT full_name FROM employees ORDER BY salary DESC",
		"SELECT dept FROM employees GROUP BY dept HAVING sum(salary) > 10",
		"SELECT full_name FROM employees WHERE dept = 'x' AND salary < 5",
		"SELECT max(salary) FROM employees",
		"SELECT full_name FROM employees ORDER BY salary",
	} {
		if err := ValidateReferences(sql, allows, restricted, allowsColumn); err == nil {
			t.Errorf("excluded column read without refusal: %s", sql)
		}
	}
}

// Qualification is resolved through the alias, which is how the model writes
// real SQL.
func TestAliasesResolveToTheirTable(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(tbl string) bool { return tbl == "employees" }
	allowsColumn := func(tbl, col string) bool {
		if tbl != "employees" {
			return true
		}
		return col == "full_name" || col == "dept_id"
	}

	ok := "SELECT e.full_name, d.name FROM employees e JOIN departments d ON d.id = e.dept_id"
	if err := ValidateReferences(ok, allows, restricted, allowsColumn); err != nil {
		t.Fatalf("a legitimate two-table join was refused: %v", err)
	}

	bad := "SELECT e.salary, d.name FROM employees e JOIN departments d ON d.id = e.dept_id"
	err := ValidateReferences(bad, allows, restricted, allowsColumn)
	if err == nil {
		t.Fatal("an excluded column read through an alias was allowed")
	}
	if !strings.Contains(err.Error(), "salary") {
		t.Errorf("refusal does not name the column: %v", err)
	}
}

// The unattributable case. With two tables in play and a bare column name,
// deciding which table it belongs to is a schema question, and guessing it
// either refuses a legitimate query or admits a restricted one. It refuses and
// says how to fix it.
func TestABareColumnAcrossTwoTablesIsRefusedWithTheRemedy(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(tbl string) bool { return tbl == "employees" }
	allowsColumn := func(string, string) bool { return true }

	sql := "SELECT full_name FROM employees e JOIN departments d ON d.id = e.dept_id"
	err := ValidateReferences(sql, allows, restricted, allowsColumn)
	if err == nil {
		t.Fatal("a bare column across two tables was attributed rather than refused")
	}
	if !strings.Contains(err.Error(), "Qualify") {
		t.Errorf("refusal does not name the remedy: %v", err)
	}
}

// Reading a restricted table through a subquery or CTE hides which column came
// from where. It is refused rather than attributed.
func TestAColumnReadThroughADerivedTableIsRefused(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(tbl string) bool { return tbl == "employees" }
	allowsColumn := func(_, col string) bool { return col == "full_name" }

	sql := "SELECT x.salary FROM (SELECT salary FROM employees) x"
	if err := ValidateReferences(sql, allows, restricted, allowsColumn); err == nil {
		t.Error("a restricted column laundered through a derived table was allowed")
	}
}

// **The cost has to fall only on tenants who asked for it.** A source with no
// column rules must behave exactly as it did before this half existed, which is
// the property that keeps ordinary analytical SQL working for everyone else.
func TestOrdinaryAnalyticalSQLIsUntouchedWithoutColumnRules(t *testing.T) {
	allows := func(string) bool { return true }
	noColumnRules := func(string) bool { return false }
	deny := func(string, string) bool { return false } // must never be consulted

	for _, sql := range []string{
		"SELECT date_trunc('month', s.created_at) AS m, sum(s.amount) FROM fact_sales s GROUP BY 1 ORDER BY 1",
		"WITH recent AS (SELECT * FROM fact_sales WHERE created_at > '2024-01-01') SELECT count(*) FROM recent",
		"SELECT extract(year FROM created_at) AS y, count(1) FROM fact_sales GROUP BY y",
		"SELECT p.category, sum(s.amount)::numeric FROM fact_sales s JOIN dim_products p ON p.id = s.product_id GROUP BY p.category",
		"SELECT CASE WHEN amount > 100 THEN 'big' ELSE 'small' END AS bucket, count(1) FROM fact_sales GROUP BY bucket",
	} {
		if err := ValidateReferences(sql, allows, noColumnRules, deny); err != nil {
			t.Errorf("a source with no column rules paid for the column half: %q → %v", sql, err)
		}
	}
}

// And on a table that *is* restricted, ordinary analytical shapes over the
// allowed columns must still run — otherwise the feature is a table ban with
// extra steps.
func TestAnalyticalSQLOverAllowedColumnsStillRuns(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(tbl string) bool { return tbl == "orders" }
	allowed := map[string]bool{"created_at": true, "amount": true, "status": true, "id": true}
	allowsColumn := func(_, col string) bool { return allowed[col] }

	for _, sql := range []string{
		"SELECT date_trunc('month', created_at) AS m, sum(amount) FROM orders GROUP BY m ORDER BY m",
		"SELECT status, count(1) FROM orders WHERE created_at > '2024-01-01' GROUP BY status",
		"SELECT extract(year FROM created_at) AS y, sum(amount)::numeric FROM orders GROUP BY y",
		"SELECT CASE WHEN amount > 100 THEN 'big' ELSE 'small' END AS bucket, count(1) FROM orders GROUP BY bucket",
		"SELECT o.status, sum(o.amount) FROM orders o GROUP BY o.status HAVING sum(o.amount) > 10",
	} {
		if err := ValidateReferences(sql, allows, restricted, allowsColumn); err != nil {
			t.Errorf("legitimate analytical SQL over allowed columns refused: %q → %v", sql, err)
		}
	}
}

func TestSplitQualifiedAndStripCast(t *testing.T) {
	for _, tc := range []struct{ in, q, c string }{
		{"e.salary", "e", "salary"},
		{"public.employees.salary", "employees", "salary"},
		{`"E"."Salary"`, "e", "salary"},
	} {
		q, c, ok := splitQualified(tc.in)
		if !ok || q != tc.q || c != tc.c {
			t.Errorf("splitQualified(%q) = %q,%q,%v", tc.in, q, c, ok)
		}
	}
	if got := stripCast("e.salary::numeric"); got != "e.salary" {
		t.Errorf("stripCast dropped the wrong half: %q", got)
	}
	if _, _, ok := splitQualified("salary"); ok {
		t.Error("an unqualified name was read as qualified")
	}
}

// A `FROM` inside a function's argument list is that function's syntax, not a
// table clause.
//
// **This is a defect in the table half, found by the column half's tests and
// older than both.** `extract(year FROM created_at)` reported `created_at` as a
// table and `substring(name FROM 1 FOR 3)` reported `1`, so on any
// table-restricted source those two ordinary shapes were refused with
// `table "created_at" is not readable by this agent on this source` — a
// sentence that names something the tenant never restricted and that no
// rewrite the model can think of will fix. The 08-22 gate did not find it
// because none of its thirteen refusal shapes used a function that spells a
// clause keyword.
func TestAFromInsideAFunctionIsNotATableClause(t *testing.T) {
	for _, tt := range []struct {
		sql  string
		want string
	}{
		{"SELECT extract(year FROM created_at) AS y, count(1) FROM orders GROUP BY y", "orders"},
		{"SELECT substring(name from 1 for 3) FROM orders", "orders"},
		{"SELECT trim(both ' ' from name) FROM orders", "orders"},
		// The control: a real subquery in FROM position is still read, so the
		// fix did not buy its correctness by going blind.
		{"SELECT x.n FROM (SELECT count(1) AS n FROM orders) x", "orders"},
	} {
		got := ReferencedTables(tt.sql)
		if got.Uncertain {
			t.Errorf("%q → uncertain: %s", tt.sql, got.UncertainReason)
			continue
		}
		if strings.Join(got.Tables, ",") != tt.want {
			t.Errorf("%q → tables %v, want [%s]", tt.sql, got.Tables, tt.want)
		}
	}
}

// The table half must refuse an excluded table that a function's FROM would
// have hidden — the fix above must not have created a way through.
func TestTheFunctionFromFixIsNotABypass(t *testing.T) {
	allows := func(tbl string) bool { return tbl == "orders" }
	// A genuine subquery reading a forbidden table, wrapped so its FROM sits
	// inside parentheses that follow an identifier.
	sql := "SELECT o.id FROM orders o WHERE o.id IN (SELECT employee_id FROM salaries)"
	if err := ValidateReferences(sql, allows, nil, nil); err == nil {
		t.Fatal("a forbidden table inside an IN-subquery was allowed")
	}
}
