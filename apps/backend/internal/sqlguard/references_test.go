package sqlguard

import (
	"strings"
	"testing"
)

// The allowlist is only as good as this file. A blocklist that misses a token
// misses an attack; an allowlist that misses one *admits* a read the tenant was
// told could not happen — so the cases below are weighted towards the shapes
// where a lexer is most likely to be quietly wrong, and every one of those is
// asserted to come back Uncertain rather than empty.

func TestReferencedTablesReadsOrdinarySQL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"one table", "SELECT count(*) FROM fact_sales", []string{"fact_sales"}},
		{"aliased", "SELECT fs.id FROM fact_sales fs", []string{"fact_sales"}},
		{"aliased with AS", "SELECT fs.id FROM fact_sales AS fs", []string{"fact_sales"}},
		{
			"join",
			"SELECT sum(fs.sales_amount) FROM fact_sales fs JOIN dim_date d ON d.date_id = fs.date_id",
			[]string{"dim_date", "fact_sales"},
		},
		{
			"three joins",
			`SELECT 1 FROM fact_sales fs
			 LEFT JOIN dim_date d ON d.date_id = fs.date_id
			 INNER JOIN dim_products p ON p.product_id = fs.product_id`,
			[]string{"dim_date", "dim_products", "fact_sales"},
		},
		// Schema qualification must not be an escape hatch: the allowlist entry
		// says `fact_sales` and this has to compare equal to it.
		{"schema qualified", "SELECT 1 FROM public.fact_sales", []string{"fact_sales"}},
		{"quoted", `SELECT 1 FROM "Fact_Sales"`, []string{"fact_sales"}},
		{"backticked (mysql)", "SELECT 1 FROM `fact_sales`", []string{"fact_sales"}},
		{"bracketed (sql server)", "SELECT 1 FROM [fact_sales]", []string{"fact_sales"}},
		// A table named in a comment or a string is not a reference. Both of
		// these would be a false refusal, which costs a tenant a working query.
		{"table named in a comment", "SELECT 1 FROM fact_sales -- not from dim_date", []string{"fact_sales"}},
		{"table named in a literal", "SELECT 1 FROM fact_sales WHERE note = 'from salaries'", []string{"fact_sales"}},
		// No FROM at all: a connectivity probe, and not uncertainty.
		{"constant select", "SELECT 1", nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := ReferencedTables(tt.sql)
			if got.Uncertain {
				t.Fatalf("Uncertain on ordinary SQL: %s", got.UncertainReason)
			}
			if strings.Join(got.Tables, ",") != strings.Join(tt.want, ",") {
				t.Errorf("tables = %v, want %v", got.Tables, tt.want)
			}
		})
	}
}

// The bypass this whole mechanism has to survive: wrap a forbidden table in a
// CTE and select from the CTE's allowed-looking name. If `recent` were reported
// as the reference, an allowlist containing `recent` would admit `salaries`.
func TestCTENamesResolveToTheirUnderlyingTables(t *testing.T) {
	got := ReferencedTables(`
		WITH recent AS (SELECT * FROM salaries WHERE paid_at > now() - interval '30 days')
		SELECT count(*) FROM recent`)
	if got.Uncertain {
		t.Fatalf("Uncertain: %s", got.UncertainReason)
	}
	if len(got.Tables) != 1 || got.Tables[0] != "salaries" {
		t.Errorf("tables = %v, want [salaries] — a CTE name must not stand in for what it reads", got.Tables)
	}
}

func TestMultipleCTEsAllResolve(t *testing.T) {
	got := ReferencedTables(`
		WITH a AS (SELECT * FROM fact_sales),
		     b AS (SELECT * FROM salaries)
		SELECT * FROM a JOIN b ON a.id = b.id`)
	if got.Uncertain {
		t.Fatalf("Uncertain: %s", got.UncertainReason)
	}
	want := "fact_sales,salaries"
	if strings.Join(got.Tables, ",") != want {
		t.Errorf("tables = %v, want [%s]", got.Tables, want)
	}
}

// A subquery in FROM position is followed by its own FROM, which the same loop
// picks up. It must not be reported as uncertainty, and the inner table must be
// found.
func TestDerivedTablesReportTheirInnerTable(t *testing.T) {
	got := ReferencedTables(`SELECT * FROM (SELECT id FROM salaries) x`)
	if got.Uncertain {
		t.Fatalf("Uncertain on a derived table: %s", got.UncertainReason)
	}
	if len(got.Tables) != 1 || got.Tables[0] != "salaries" {
		t.Errorf("tables = %v, want [salaries]", got.Tables)
	}
}

// The property the design turns on: what this cannot read, it says it cannot
// read. Each of these must be Uncertain — never silently empty, which
// ValidateReferences would pass.
func TestUnreadableShapesAreReportedAsUncertain(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"FROM at the end of the statement", "SELECT 1 FROM"},
		{"FROM followed by punctuation", "SELECT 1 FROM , x"},
		{"FROM followed by an operator", "SELECT 1 FROM = x"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := ReferencedTables(tt.sql)
			if !got.Uncertain {
				t.Errorf("ReferencedTables(%q) = %v, want Uncertain — an unreadable statement that reports no tables would be admitted", tt.sql, got.Tables)
			}
		})
	}
}

func TestValidateReferencesRefusesAnExcludedTable(t *testing.T) {
	allows := func(t string) bool { return t == "fact_sales" }

	if err := ValidateReferences("SELECT 1 FROM fact_sales", allows, nil); err != nil {
		t.Errorf("allowed table refused: %v", err)
	}

	err := ValidateReferences("SELECT 1 FROM fact_sales JOIN salaries s ON s.id = 1", allows, nil)
	if err == nil {
		t.Fatal("a join onto an excluded table was allowed")
	}
	if !strings.Contains(err.Error(), "salaries") {
		t.Errorf("refusal does not name the table it refused: %v", err)
	}
	// The tenant's allowlist must not be recited into a refusal a
	// prompt-injected turn can read back.
	if strings.Contains(err.Error(), "fact_sales") {
		t.Errorf("refusal lists what IS allowed: %v", err)
	}
}

func TestValidateReferencesRefusesWhatItCannotRead(t *testing.T) {
	err := ValidateReferences("SELECT 1 FROM", func(string) bool { return true }, nil)
	if err == nil {
		t.Fatal("an unreadable statement was allowed against a restricted source")
	}
}

// A star names no column and expands at the database to every one of them, so
// it cannot be inspected — only refused.
func TestValidateReferencesRefusesStarOnAColumnRestrictedTable(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(t string) bool { return t == "employees" }

	if err := ValidateReferences("SELECT * FROM fact_sales", allows, restricted); err != nil {
		t.Errorf("star on an unrestricted table refused: %v", err)
	}
	err := ValidateReferences("SELECT * FROM employees", allows, restricted)
	if err == nil {
		t.Fatal("SELECT * against a column-restricted table was allowed")
	}
	if !strings.Contains(err.Error(), "name the columns you need") {
		t.Errorf("refusal does not say what to do instead: %v", err)
	}
	// Naming columns is the repair, and it has to work.
	if err := ValidateReferences("SELECT full_name FROM employees", allows, restricted); err != nil {
		t.Errorf("named columns refused: %v", err)
	}
}

// The two bypasses this lexer's first cut actually had, found by probing it
// rather than by reading it. Both admitted an excluded table with no refusal
// and no uncertainty, which is the one outcome the design is supposed to make
// impossible — so they are pinned here rather than left to the reviewer who
// notices the comma.
func TestOldStyleCommaJoinsAreNotABypass(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		// The first bypass: FROM introduces a list, and reading only its head
		// admitted everything after the comma.
		{"bare", "SELECT 1 FROM fact_sales, salaries", "fact_sales,salaries"},
		{"aliased", "SELECT 1 FROM fact_sales a, salaries b WHERE a.id = b.id", "fact_sales,salaries"},
		// The second: `, name AS` is also how a CTE binds, so the CTE collector
		// claimed `salaries` and the reference list dropped it.
		{"aliased with AS", "SELECT 1 FROM fact_sales AS a, salaries AS b", "fact_sales,salaries"},
		{"three", "SELECT 1 FROM a AS x, b AS y, c AS z", "a,b,c"},
		{"subquery first", "SELECT 1 FROM (SELECT id FROM salaries) x, fact_sales", "fact_sales,salaries"},
		{"subquery second", "SELECT 1 FROM fact_sales, (SELECT id FROM salaries) y", "fact_sales,salaries"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := ReferencedTables(tt.sql)
			if got.Uncertain {
				t.Fatalf("Uncertain: %s", got.UncertainReason)
			}
			if strings.Join(got.Tables, ",") != tt.want {
				t.Errorf("tables = %v, want [%s]", got.Tables, tt.want)
			}
		})
	}
}

// The CTE walk must stay anchored to a leading WITH. These are the forms it has
// to keep reading correctly now that it is no longer matching on any comma.
func TestCTEWalkHandlesTheRealForms(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{"recursive", "WITH RECURSIVE t AS (SELECT 1 FROM salaries) SELECT * FROM t", "salaries"},
		{"column list", "WITH t (x, y) AS (SELECT 1, 2 FROM salaries) SELECT * FROM t", "salaries"},
		{"materialized", "WITH t AS MATERIALIZED (SELECT 1 FROM salaries) SELECT * FROM t", "salaries"},
		{
			"two bindings, both resolved",
			"WITH a AS (SELECT 1 FROM salaries), b AS (SELECT 1 FROM payroll) SELECT * FROM a, b",
			"payroll,salaries",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := ReferencedTables(tt.sql)
			if got.Uncertain {
				t.Fatalf("Uncertain: %s", got.UncertainReason)
			}
			if strings.Join(got.Tables, ",") != tt.want {
				t.Errorf("tables = %v, want [%s]", got.Tables, tt.want)
			}
		})
	}
}

// Subqueries in every position a model actually writes them. Each inner table
// must be found: a WHERE … IN (SELECT … FROM salaries) reads salaries.
func TestSubqueriesInEveryPositionAreFound(t *testing.T) {
	cases := map[string]string{
		"scalar in the select list": "SELECT (SELECT max(pay) FROM salaries) FROM fact_sales",
		"IN predicate":              "SELECT 1 FROM fact_sales WHERE id IN (SELECT id FROM salaries)",
		"derived table":             "SELECT * FROM (SELECT id FROM salaries) x",
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			got := ReferencedTables(sql)
			if got.Uncertain {
				t.Fatalf("Uncertain: %s", got.UncertainReason)
			}
			var found bool
			for _, tbl := range got.Tables {
				if tbl == "salaries" {
					found = true
				}
			}
			if !found {
				t.Errorf("tables = %v, want salaries among them", got.Tables)
			}
		})
	}
}

// UNION is two statements' worth of tables in one statement, and both halves
// have to be checked.
func TestUnionArmsAreBothRead(t *testing.T) {
	got := ReferencedTables("SELECT 1 FROM fact_sales UNION SELECT 1 FROM salaries")
	if got.Uncertain {
		t.Fatalf("Uncertain: %s", got.UncertainReason)
	}
	if strings.Join(got.Tables, ",") != "fact_sales,salaries" {
		t.Errorf("tables = %v, want both arms", got.Tables)
	}
}

// The §1q live gate's false-positive arm. `containsSelectStar` is crude in the
// safe direction on purpose — `count(*)` sets it — and the argument for that
// is sound, but the refusal it produced told the model to do the one thing
// that does not fix it: "Name the columns you need" on a query whose select
// list is `p.category, count(*)` is advice the model can follow to the letter
// and be refused again. Ten other tool paths in this repo have already
// produced a retry loop out of exactly that shape.
//
// The rule does not move. The sentence names the remedy that works.
func TestStarRefusalNamesTheRemedyForCountStar(t *testing.T) {
	allows := func(string) bool { return true }
	restricted := func(table string) bool { return table == "dim_products" }

	sql := "SELECT p.category, count(*) AS n FROM fact_sales s JOIN dim_products p ON p.product_id = s.product_id GROUP BY p.category"
	err := ValidateReferences(sql, allows, restricted)
	if err == nil {
		t.Fatal("a statement containing `*` against a column-restricted table was allowed; the rule is crude in the safe direction and must stay that way")
	}
	msg := err.Error()
	if !strings.Contains(msg, "count(1)") {
		t.Errorf("the refusal does not name the rewrite that works: %s", msg)
	}
	if !strings.Contains(msg, "count(*)") {
		t.Errorf("the refusal does not say that count(*) is what tripped it: %s", msg)
	}
}
