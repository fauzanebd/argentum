package eval

import (
	"path/filepath"
	"testing"
)

// The golden set is data, and data with a typo in it silently stops testing
// what it claims to test. This runs in `go test ./...`, so a broken case is
// caught in CI rather than fifteen minutes into an eval run.
func TestShippedGoldenSetIsValid(t *testing.T) {
	set, err := LoadSet(filepath.Join("..", "..", "testdata", "eval", "golden.yaml"))
	if err != nil {
		t.Fatalf("golden set does not load: %v", err)
	}

	if len(set.Cases) < 30 {
		t.Errorf("golden set has %d cases, T-01 requires at least 30", len(set.Cases))
	}

	// Minimum coverage per category, from the ticket. Without this the set
	// can drift into thirty variations of "what were total sales".
	required := map[string]int{
		"simple_aggregate": 6,
		"time_window":      5,
		"grouping_topn":    4,
		"multi_source":     3,
		"chart_dashboard":  3,
		"indonesian":       5,
		"guardrail":        4,
	}
	got := set.Categories()
	for cat, min := range required {
		if got[cat] < min {
			t.Errorf("category %q has %d cases, need at least %d", cat, got[cat], min)
		}
	}

	// Every Indonesian case must actually assert an Indonesian reply,
	// otherwise the category tests nothing the English ones do not.
	for _, c := range set.Cases {
		if c.Category == "indonesian" && c.Lang != "id" {
			t.Errorf("case %q is in the indonesian category but expects lang %q", c.ID, c.Lang)
		}
	}
}

func TestValidateRejectsBrokenCases(t *testing.T) {
	tests := []struct {
		name string
		set  Set
	}{
		{"no cases", Set{}},
		{"missing id", Set{Cases: []Case{{Question: "q", Lang: "en", Expect: Expect{Kind: KindNumeric}}}}},
		{"missing question", Set{Cases: []Case{{ID: "a", Lang: "en", Expect: Expect{Kind: KindNumeric}}}}},
		{"bad lang", Set{Cases: []Case{{ID: "a", Question: "q", Lang: "fr", Expect: Expect{Kind: KindNumeric}}}}},
		{"unknown kind", Set{Cases: []Case{{ID: "a", Question: "q", Lang: "en", Expect: Expect{Kind: "vibes"}}}}},
		{"contains asserts nothing", Set{Cases: []Case{{ID: "a", Question: "q", Lang: "en", Expect: Expect{Kind: KindContains}}}}},
		{"sql_shape without patterns", Set{Cases: []Case{{ID: "a", Question: "q", Lang: "en", Expect: Expect{Kind: KindSQLShape}}}}},
		{"duplicate ids", Set{Cases: []Case{
			{ID: "a", Question: "q", Lang: "en", Expect: Expect{Kind: KindNumeric}},
			{ID: "a", Question: "q2", Lang: "en", Expect: Expect{Kind: KindNumeric}},
		}}},
		// A report case whose format is not one — the directive would name a
		// format generate_document refuses, and the case would fail for a
		// reason that has nothing to do with what it asserts (T-A2b).
		{"unknown report format", Set{Cases: []Case{
			{ID: "a", Question: "q", Lang: "en", ReportFormat: "docx", Expect: Expect{Kind: KindNumeric}},
		}}},
	}
	for _, tt := range tests {
		if err := tt.set.Validate(); err == nil {
			t.Errorf("%s: Validate accepted an invalid set", tt.name)
		}
	}
}

func TestValidateDefaultsNumericTolerance(t *testing.T) {
	s := Set{Cases: []Case{{ID: "a", Question: "q", Lang: "en", Expect: Expect{Kind: KindNumeric, Value: 10}}}}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if s.Cases[0].Expect.Tolerance != 0.01 {
		t.Errorf("tolerance defaulted to %v, want 0.01", s.Cases[0].Expect.Tolerance)
	}
}

func TestFilterByIDAndCategory(t *testing.T) {
	s := Set{Cases: []Case{
		{ID: "a", Category: "guardrail", Question: "q", Lang: "en", Expect: Expect{Kind: KindNumeric}},
		{ID: "b", Category: "indonesian", Question: "q", Lang: "id", Expect: Expect{Kind: KindNumeric}},
		{ID: "c", Category: "indonesian", Question: "q", Lang: "id", Expect: Expect{Kind: KindNumeric}},
	}}
	if got := len(s.Filter([]string{"indonesian"})); got != 2 {
		t.Errorf("category filter returned %d cases, want 2", got)
	}
	if got := len(s.Filter([]string{"a"})); got != 1 {
		t.Errorf("id filter returned %d cases, want 1", got)
	}
	if got := len(s.Filter(nil)); got != 3 {
		t.Errorf("empty filter returned %d cases, want all 3", got)
	}
}
