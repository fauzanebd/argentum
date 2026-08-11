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
		// T-Q1's five, 2026-08-11. The set read 40/40 for nine days, which is a
		// set that cannot rank two prompts — these are the categories the
		// baseline's own "what this baseline is not" section named as absent.
		"follow_up":       3,
		"zero_row_trap":   2,
		"wrong_grain":     3,
		"no_chart_wanted": 3,
		"dirty_schema":    2,
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

	// A follow-up case with no follow-ups is a single-turn case wearing the
	// category's name, and it would report the category as covered while
	// testing nothing multi-turn at all.
	for _, c := range set.Cases {
		if c.Category == "follow_up" && len(c.FollowUps) == 0 {
			t.Errorf("case %q is in the follow_up category and asks only one turn", c.ID)
		}
	}
}

// The point of the category is that the naive query returns rows. A wrong_grain
// case asserting a value rather than a shape would pass whenever the model got
// lucky on a seed, so the category is pinned to the instrument that can see the
// difference.
func TestWrongGrainCasesAssertSQLShape(t *testing.T) {
	set, err := LoadSet(filepath.Join("..", "..", "testdata", "eval", "golden.yaml"))
	if err != nil {
		t.Fatalf("golden set does not load: %v", err)
	}
	for _, c := range set.Cases {
		if c.Category != "wrong_grain" {
			continue
		}
		if c.Expect.Kind != KindSQLShape {
			t.Errorf("case %q is wrong_grain but asserts %q, not sql_shape", c.ID, c.Expect.Kind)
		}
	}
}

func TestQuestionsIncludesFollowUpsInOrder(t *testing.T) {
	c := Case{Question: "first", FollowUps: []string{"second", "third"}}
	got := c.Questions()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("Questions() returned %d turns, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d is %q, want %q", i, got[i], want[i])
		}
	}

	// A case with no follow-ups is still one turn, not zero. RunCase loops over
	// this, so an empty slice here would run nothing and score an empty reply.
	if got := (Case{Question: "only"}).Questions(); len(got) != 1 || got[0] != "only" {
		t.Errorf("single-turn Questions() = %v, want [only]", got)
	}
}

func TestValidateRejectsBrokenFollowUps(t *testing.T) {
	tests := []struct {
		name string
		set  Set
	}{
		{"empty follow-up", Set{Cases: []Case{{
			ID: "a", Question: "q", Lang: "en", FollowUps: []string{"  "},
			Expect: Expect{Kind: KindNumeric},
		}}}},
		// The report directive is written for one turn producing one file.
		{"report format with follow-ups", Set{Cases: []Case{{
			ID: "a", Question: "q", Lang: "en", ReportFormat: "pdf", FollowUps: []string{"and again"},
			Expect: Expect{Kind: KindNumeric},
		}}}},
	}
	for _, tt := range tests {
		if err := tt.set.Validate(); err == nil {
			t.Errorf("%s: Validate accepted an invalid set", tt.name)
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
