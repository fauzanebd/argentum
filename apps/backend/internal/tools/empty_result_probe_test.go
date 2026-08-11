package tools

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

func TestParseEqualityFiltersFindsWhatWasFilteredOn(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		column string
		value  string
	}{
		{
			// The E-5 landmine itself: month_name was seeded with TO_CHAR(d,
			// 'Month'), which pads to nine characters, so the stored value is
			// 'December ' and this returns nothing from 310 December rows.
			name:   "the padded-label case",
			sql:    "SELECT SUM(s.sales_amount) FROM fact_sales s JOIN dim_date d ON d.date_id = s.date_id WHERE d.month_name = 'December'",
			column: "month_name",
			value:  "December",
		},
		{
			name:   "unqualified column",
			sql:    "SELECT * FROM t WHERE city = 'Reykjavik'",
			column: "city",
			value:  "Reykjavik",
		},
		{
			name:   "LIKE",
			sql:    "SELECT * FROM t WHERE category LIKE 'Electronics'",
			column: "category",
			value:  "Electronics",
		},
		{
			name:   "IN list takes the first literal",
			sql:    "SELECT * FROM t WHERE channel IN ('Onlin', 'Instore')",
			column: "channel",
			value:  "Onlin",
		},
	}
	for _, tt := range tests {
		got := parseEqualityFilters(tt.sql)
		if len(got) == 0 {
			t.Errorf("%s: no filter found in %q", tt.name, tt.sql)
			continue
		}
		if got[0].column != tt.column || got[0].value != tt.value {
			t.Errorf("%s: got %s=%q, want %s=%q",
				tt.name, got[0].column, got[0].value, tt.column, tt.value)
		}
	}
}

// A literal in a SELECT list or a JOIN condition is not what the user filtered
// on, and probing it would answer a question nobody asked.
func TestParseEqualityFiltersIgnoresLiteralsOutsideWhere(t *testing.T) {
	sql := "SELECT 'Online' AS label FROM fact_sales s JOIN dim_x x ON x.tag = 'fixed'"
	if got := parseEqualityFilters(sql); len(got) != 0 {
		t.Errorf("found %+v outside a WHERE clause", got)
	}
}

func TestParseEqualityFiltersDedupesColumns(t *testing.T) {
	sql := "SELECT * FROM t WHERE city = 'A' OR city = 'B'"
	if got := parseEqualityFilters(sql); len(got) != 1 {
		t.Errorf("got %d filters for one column, want 1: %+v", len(got), got)
	}
}

func TestTableHoldingResolvesTheColumn(t *testing.T) {
	meta := &db.SchemaMetadata{Tables: []db.TableInfo{
		{Name: "fact_sales", Columns: []db.ColumnInfo{{Name: "sales_amount"}}},
		{Name: "dim_date", Columns: []db.ColumnInfo{{Name: "month_name"}}},
	}}
	if got, ok := tableHolding(meta, "month_name"); !ok || got != "dim_date" {
		t.Errorf("tableHolding = %q, %v; want dim_date, true", got, ok)
	}
	// Case-insensitive, because the model writes MONTH_NAME as often as not.
	if got, ok := tableHolding(meta, "MONTH_NAME"); !ok || got != "dim_date" {
		t.Errorf("case-insensitive lookup failed: %q, %v", got, ok)
	}
	if _, ok := tableHolding(meta, "no_such_column"); ok {
		t.Error("tableHolding invented a table for an unknown column")
	}
}

// The two identifiers cannot be parameters — a placeholder stands for a value,
// never for a table or a column — so the shape is checked at the point of use
// even though both come from our own schema metadata.
func TestTheProbeRefusesNonIdentifiers(t *testing.T) {
	for _, bad := range []string{
		"users; DROP TABLE users",
		"users WHERE 1=1--",
		"",
		"a b",
		"1table",
		`"quoted"`,
	} {
		if identifier.MatchString(bad) {
			t.Errorf("identifier accepted %q", bad)
		}
	}
	for _, good := range []string{"fact_sales", "dim_date", "_private", "T1"} {
		if !identifier.MatchString(good) {
			t.Errorf("identifier rejected the legitimate name %q", good)
		}
	}
}

// The whole point of the probe is a value whose stored form differs by
// whitespace or case. Unquoted, `December ` and `December` are indistinguishable
// — which is exactly how the bug survived three months of demos.
func TestProbeNoteAndPayloadTellTheAgentWhatToDo(t *testing.T) {
	payload := map[string]interface{}{"row_count": 0}
	attachProbe(payload, []map[string]interface{}{{
		"column":           "month_name",
		"table":            "dim_date",
		"you_filtered_for": "December",
		"actual_values":    []string{`"December "`, `"November "`},
	}})

	note, _ := payload["note"].(string)
	if !strings.Contains(note, "ACTUALLY contain") {
		t.Errorf("the note does not point at the real values: %q", note)
	}
	if !strings.Contains(note, "Do NOT state a total") {
		t.Error("the probe note dropped the zero-row warning it replaces")
	}
	if payload["available_values"] == nil {
		t.Error("the probe findings did not reach the payload")
	}
}

func TestAttachProbeIsANoOpWithNoFindings(t *testing.T) {
	payload := map[string]interface{}{"note": "original"}
	attachProbe(payload, nil)
	if payload["note"] != "original" {
		t.Error("an empty probe overwrote the zero-row note")
	}
	if _, ok := payload["available_values"]; ok {
		t.Error("an empty probe added a field")
	}
}
