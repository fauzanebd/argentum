package domain

import (
	"errors"
	"testing"
)

// The empty case is the one that matters most, and it is the one an
// allowlist implementation gets backwards. Migration 068 gives every existing
// `db_connections` row an empty allowlist; if empty meant deny-all, every
// tenant's agent would stop being able to read anything the moment the
// migration applied.
func TestAnEmptyAllowlistIsUnrestricted(t *testing.T) {
	var a Allowlist
	if a.Restricted() {
		t.Error("the zero allowlist reports as restricted")
	}
	if !a.AllowsTable("anything") {
		t.Error("the zero allowlist refuses a table")
	}
	if !a.AllowsColumn("anything", "at_all") {
		t.Error("the zero allowlist refuses a column")
	}
	if a.ColumnsRestricted("anything") {
		t.Error("the zero allowlist reports a table's columns as restricted")
	}
}

func TestAllowsTable(t *testing.T) {
	a := Allowlist{Tables: []string{"fact_sales", "dim_date"}}

	for _, name := range []string{
		"fact_sales",
		"FACT_SALES",        // SQL identifiers are case-insensitive
		"public.fact_sales", // and an allowlist evadable by prefixing the schema is not one
		`"fact_sales"`,
		"`fact_sales`",
		"[fact_sales]",
		`"public"."Fact_Sales"`,
	} {
		if !a.AllowsTable(name) {
			t.Errorf("AllowsTable(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"salaries", "fact_sales_archive", "", "dim"} {
		if a.AllowsTable(name) {
			t.Errorf("AllowsTable(%q) = true, want false", name)
		}
	}
}

func TestAllowsColumn(t *testing.T) {
	a := Allowlist{
		Tables:  []string{"employees", "fact_sales"},
		Columns: map[string][]string{"employees": {"full_name", "department"}},
	}

	// A table with a column rule exposes exactly what is listed.
	if !a.AllowsColumn("employees", "full_name") {
		t.Error("a listed column was refused")
	}
	if !a.AllowsColumn("employees", "DEPARTMENT") {
		t.Error("column matching is case-sensitive and should not be")
	}
	if a.AllowsColumn("employees", "monthly_salary") {
		t.Error("an unlisted column on a restricted table was allowed")
	}
	// A table with no rule exposes everything.
	if !a.AllowsColumn("fact_sales", "anything") {
		t.Error("a table with no column rule restricted its columns")
	}
	// A table outside the allowlist exposes nothing, rule or no rule.
	if a.AllowsColumn("salaries", "amount") {
		t.Error("a column of an excluded table was allowed")
	}

	if !a.ColumnsRestricted("employees") {
		t.Error("employees has a column rule and does not report as restricted")
	}
	if a.ColumnsRestricted("fact_sales") {
		t.Error("fact_sales has no column rule and reports as restricted")
	}
}

// Every rule here refuses configuration that reads as a permission and grants
// nothing. Each one produces the same support ticket if stored silently.
func TestValidateRefusesConfigurationThatCannotMeanWhatItSays(t *testing.T) {
	cases := []struct {
		name string
		list Allowlist
	}{
		{"empty table name", Allowlist{Tables: []string{"fact_sales", "  "}}},
		{"duplicate table", Allowlist{Tables: []string{"fact_sales", "FACT_SALES"}}},
		{
			"column rule on an excluded table",
			Allowlist{Tables: []string{"fact_sales"}, Columns: map[string][]string{"employees": {"full_name"}}},
		},
		{
			"a table allowed with no readable column",
			Allowlist{Tables: []string{"employees"}, Columns: map[string][]string{"employees": {}}},
		},
		{
			"duplicate column",
			Allowlist{Tables: []string{"employees"}, Columns: map[string][]string{"employees": {"a", "A"}}},
		},
		{
			"empty column name",
			Allowlist{Tables: []string{"employees"}, Columns: map[string][]string{"employees": {""}}},
		},
		{"empty table key on a column rule", Allowlist{Columns: map[string][]string{"": {"a"}}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.list.Validate()
			if err == nil {
				t.Fatal("Validate accepted it")
			}
			// One family, so a handler can turn all of them into one 400.
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("error is not ErrInvalidInput: %v", err)
			}
		})
	}
}

func TestValidateAcceptsTheRealShapes(t *testing.T) {
	cases := []struct {
		name string
		list Allowlist
	}{
		{"nothing configured", Allowlist{}},
		{"tables only", Allowlist{Tables: []string{"fact_sales", "dim_date"}}},
		{
			"tables plus a column rule",
			Allowlist{
				Tables:  []string{"employees", "fact_sales"},
				Columns: map[string][]string{"employees": {"full_name", "department"}},
			},
		},
		// Columns with no table list: every table is readable, one of them
		// partially. A legitimate configuration — "they can see everything
		// except the salary column" — and the one most likely to be refused by
		// a validator that assumed Tables is always set.
		{
			"a column rule with no table list",
			Allowlist{Columns: map[string][]string{"employees": {"full_name"}}},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.list.Validate(); err != nil {
				t.Errorf("Validate rejected a legitimate allowlist: %v", err)
			}
		})
	}
}

// The configuration above has to behave the way its name claims: with no table
// list, every table is readable and only the named one is narrowed.
func TestColumnRuleWithNoTableListNarrowsOnlyThatTable(t *testing.T) {
	a := Allowlist{Columns: map[string][]string{"employees": {"full_name"}}}

	if !a.Restricted() {
		t.Error("a column-only allowlist reports as unrestricted")
	}
	if !a.AllowsTable("anything_else") {
		t.Error("a column-only allowlist refused an unrelated table")
	}
	if !a.AllowsColumn("anything_else", "any_column") {
		t.Error("a column-only allowlist refused an unrelated table's column")
	}
	if a.AllowsColumn("employees", "monthly_salary") {
		t.Error("the narrowed table exposed an unlisted column")
	}
}
