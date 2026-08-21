package tools

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// T-H12's two halves meet here: get_schema must not name what run_sql will
// refuse. The ticket calls the mismatch "the most confusing failure available",
// and it is worse than confusing — an agent that can see `salaries` in the
// schema will keep writing queries against it and keep being refused, burning
// the tenant's iteration budget on a table they deliberately hid.

func demoSchema() *db.SchemaMetadata {
	return &db.SchemaMetadata{
		DBType: "postgres",
		Tables: []db.TableInfo{
			{Name: "fact_sales", Columns: []db.ColumnInfo{{Name: "id"}, {Name: "sales_amount"}}},
			{Name: "dim_date", Columns: []db.ColumnInfo{{Name: "date_id"}, {Name: "full_date"}}},
			{Name: "employees", Columns: []db.ColumnInfo{
				{Name: "employee_id"}, {Name: "full_name"}, {Name: "monthly_salary"},
			}},
		},
		Relationships: []db.Relationship{
			{FromTable: "fact_sales", ToTable: "dim_date"},
			{FromTable: "fact_sales", ToTable: "employees"},
		},
	}
}

func tableNames(s *db.SchemaMetadata) []string {
	out := make([]string, 0, len(s.Tables))
	for _, t := range s.Tables {
		out = append(out, t.Name)
	}
	return out
}

func columnNames(s *db.SchemaMetadata, table string) []string {
	for _, t := range s.Tables {
		if t.Name != table {
			continue
		}
		out := make([]string, 0, len(t.Columns))
		for _, c := range t.Columns {
			out = append(out, c.Name)
		}
		return out
	}
	return nil
}

func TestApplyAllowlistHidesExcludedTables(t *testing.T) {
	got := applyAllowlist(demoSchema(), domain.Allowlist{Tables: []string{"fact_sales", "dim_date"}})

	if names := strings.Join(tableNames(got), ","); names != "fact_sales,dim_date" {
		t.Errorf("tables = %s, want fact_sales,dim_date", names)
	}
	// A foreign key pointing at an excluded table names that table in the
	// prompt, which is exactly what the tenant asked not to happen.
	for _, rel := range got.Relationships {
		if rel.ToTable == "employees" || rel.FromTable == "employees" {
			t.Errorf("a relationship still names the excluded table: %+v", rel)
		}
	}
	if len(got.Relationships) != 1 {
		t.Errorf("kept %d relationships, want the one whose endpoints both survived", len(got.Relationships))
	}
}

func TestApplyAllowlistHidesExcludedColumns(t *testing.T) {
	got := applyAllowlist(demoSchema(), domain.Allowlist{
		Columns: map[string][]string{"employees": {"employee_id", "full_name"}},
	})

	// No table list, so every table survives.
	if len(got.Tables) != 3 {
		t.Fatalf("tables = %v, want all three", tableNames(got))
	}
	if cols := strings.Join(columnNames(got, "employees"), ","); cols != "employee_id,full_name" {
		t.Errorf("employees columns = %s, want employee_id,full_name", cols)
	}
	// A table with no rule keeps everything.
	if cols := strings.Join(columnNames(got, "fact_sales"), ","); cols != "id,sales_amount" {
		t.Errorf("fact_sales columns = %s, want them all", cols)
	}
}

// The one that ties the halves together. For every table the allowlist hides,
// a query naming it must be refused — and for every table it keeps, a query
// naming it must not be.
func TestWhatGetSchemaHidesIsWhatRunSQLRefuses(t *testing.T) {
	list := domain.Allowlist{Tables: []string{"fact_sales", "dim_date"}}
	visible := map[string]bool{}
	for _, name := range tableNames(applyAllowlist(demoSchema(), list)) {
		visible[name] = true
	}

	for _, tbl := range demoSchema().Tables {
		sql := "SELECT 1 FROM " + tbl.Name
		err := guardAllowlist(sql, list)
		switch {
		case visible[tbl.Name] && err != nil:
			t.Errorf("%q is in the schema get_schema serves and run_sql refused it: %v", tbl.Name, err)
		case !visible[tbl.Name] && err == nil:
			t.Errorf("%q is hidden from get_schema and run_sql ran it anyway", tbl.Name)
		}
	}
}

// An unrestricted source must not start paying for the reference lexer's
// refusals. Every tenant on this deployment has been running arbitrary
// analytical SQL through run_sql since T-H4 step 3; this ticket must not break
// any of it for the tenants it does not serve.
func TestAnUnrestrictedSourceIsNotChecked(t *testing.T) {
	var none domain.Allowlist
	for _, sql := range []string{
		"SELECT 1 FROM fact_sales",
		"SELECT 1 FROM",           // unreadable, and still not this tenant's problem
		"SELECT * FROM employees", // a star that a restricted source would refuse
		"SELECT 1 FROM a, b, c",   // shapes the lexer may or may not follow
	} {
		if err := guardAllowlist(sql, none); err != nil {
			t.Errorf("guardAllowlist refused %q on an unrestricted source: %v", sql, err)
		}
	}
}

// The allowlist entry an admin typed and the reference the lexer extracted are
// normalised by two functions in two packages — sqlguard must not import the
// domain. They have to agree, or a rule an admin can see is a rule that does
// not apply.
func TestTheTwoNormalisersAgree(t *testing.T) {
	// Every form the pair has to treat identically, run through both sides at
	// once: the domain's AllowsTable holds the entry, sqlguard's extractor
	// produces the reference.
	for _, form := range []string{
		"fact_sales",
		"FACT_SALES",
		"public.fact_sales",
		`"fact_sales"`,
		"`fact_sales`",
		"[fact_sales]",
		`"Public"."Fact_Sales"`,
	} {
		list := domain.Allowlist{Tables: []string{form}}
		// The reference side: whatever ReferencedTables makes of the same name.
		refs := sqlguard.ReferencedTables("SELECT 1 FROM " + form)
		if refs.Uncertain {
			t.Fatalf("ReferencedTables could not read %q: %s", form, refs.UncertainReason)
		}
		if len(refs.Tables) != 1 {
			t.Fatalf("ReferencedTables(%q) = %v, want one name", form, refs.Tables)
		}
		if !list.AllowsTable(refs.Tables[0]) {
			t.Errorf("an allowlist entry of %q does not match the reference %q extracted from the same text",
				form, refs.Tables[0])
		}
		// And the whole path, which is what actually ships.
		if err := guardAllowlist("SELECT 1 FROM "+form, list); err != nil {
			t.Errorf("guardAllowlist refused a table its own allowlist names as %q: %v", form, err)
		}
	}
}

func TestGuardAllowlistRefusesTheCTEWrapBypass(t *testing.T) {
	list := domain.Allowlist{Tables: []string{"fact_sales", "recent"}}
	err := guardAllowlist(
		"WITH recent AS (SELECT * FROM employees) SELECT count(*) FROM recent", list)
	if err == nil {
		t.Fatal("an excluded table wrapped in an allowed CTE name was admitted")
	}
	if !strings.Contains(err.Error(), "employees") {
		t.Errorf("refusal does not name the table actually read: %v", err)
	}
}
