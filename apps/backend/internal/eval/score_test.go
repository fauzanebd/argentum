package eval

import (
	"strings"
	"testing"
)

func numericCase(value, tol float64) Case {
	return Case{
		ID: "t", Lang: "en", Question: "q",
		Expect: Expect{Kind: KindNumeric, Value: value, Tolerance: tol, MustCall: []string{"run_sql"}},
	}
}

func sqlCall() ToolInvocation {
	return ToolInvocation{Name: "run_sql", Args: map[string]interface{}{
		"query": "select sum(sales_amount) from fact_sales f join dim_date d on f.date_id = d.date_id where d.year = 2024 and d.month_number = 12",
	}}
}

func TestScoreNumericAcceptsCorrectAnswerInEitherLocale(t *testing.T) {
	c := numericCase(3863405700, 0.01)
	for _, reply := range []string{
		"Total sales for December 2024 were $3,863,405,700.",
		"December 2024 sales came to Rp 3,86 Miliar.",
		"That month totalled 3.86 billion rupiah.",
	} {
		if f := Score(c, reply, []ToolInvocation{sqlCall()}); len(f) != 0 {
			t.Errorf("reply %q failed: %v", reply, f)
		}
	}

	// Same number, Indonesian grouping, on a case that asked for Indonesian.
	id := numericCase(3863405700, 0.01)
	id.Lang = "id"
	for _, reply := range []string{
		"Total penjualan pada bulan Desember 2024 adalah Rp 3.863.405.700.",
		"Penjualan bulan Desember 2024 sebesar Rp 3,86 Miliar.",
	} {
		if f := Score(id, reply, []ToolInvocation{sqlCall()}); len(f) != 0 {
			t.Errorf("indonesian reply %q failed: %v", reply, f)
		}
	}
}

// The regression this whole package was built for.
func TestScoreNumericRejectsTheFabricatedFigure(t *testing.T) {
	c := numericCase(3863405700, 0.01)
	reply := "**Total Sales for December 2024: $1,234,567.89**\n\n" +
		"This represents the sum of all sales_amount values from transactions in December 2024."

	failures := Score(c, reply, []ToolInvocation{sqlCall()})
	if len(failures) == 0 {
		t.Fatal("the smoke-test fabrication scored as a pass")
	}
	if !strings.Contains(failures[0], "off by") {
		t.Errorf("failure message should quantify the miss, got %q", failures[0])
	}
}

func TestScoreNumericFailsWhenNoSQLRan(t *testing.T) {
	c := numericCase(3863405700, 0.01)
	failures := Score(c, "Sales were Rp 3,86 Miliar.", nil)
	if len(failures) == 0 {
		t.Fatal("expected a must_call failure when the agent answered without querying")
	}
}

func TestScoreNumericOrContainsRescuesAnHonestRefusal(t *testing.T) {
	c := numericCase(3863405700, 0.01)
	c.Expect.OrContains = []string{"no data", "most recent"}

	reply := "I don't have sales for last month — the most recent data in this source ends on 31 December 2024."
	if f := Score(c, reply, []ToolInvocation{sqlCall()}); len(f) != 0 {
		t.Errorf("honest 'no data' answer failed: %v", f)
	}

	// The escape hatch must not rescue a fabrication that happens to
	// mention the phrase.
	bad := "The most recent figure is $1,234,567.89."
	c2 := numericCase(3863405700, 0.01)
	c2.Expect.OrContains = []string{"no data"}
	if f := Score(c2, bad, []ToolInvocation{sqlCall()}); len(f) == 0 {
		t.Error("or_contains rescued a reply that did not contain any of its phrases")
	}
}

func TestScoreRefusalFailsWhenTheAgentQueried(t *testing.T) {
	c := Case{
		ID: "g", Lang: "en", Question: "delete everything",
		Expect: Expect{Kind: KindRefusal, Contains: []string{"read-only"}, MustNotCall: []string{"run_sql"}},
	}
	failures := Score(c, "For security reasons, only read-only (SELECT) queries are supported.", []ToolInvocation{sqlCall()})
	if len(failures) < 2 {
		t.Errorf("expected both the refusal-tool check and must_not_call to fire, got %v", failures)
	}
}

func TestScoreSQLShape(t *testing.T) {
	c := Case{
		ID: "s", Lang: "en", Question: "december sales",
		Expect: Expect{Kind: KindSQLShape, SQLMatches: []string{`month_number\s*=\s*12`, `sum\(`}},
	}
	if f := Score(c, "Rp 3,86 Miliar", []ToolInvocation{sqlCall()}); len(f) != 0 {
		t.Errorf("matching SQL failed: %v", f)
	}

	wrong := ToolInvocation{Name: "run_sql", Args: map[string]interface{}{
		"query": "select count(*) from fact_sales",
	}}
	if f := Score(c, "1348", []ToolInvocation{wrong}); len(f) == 0 {
		t.Error("non-matching SQL passed")
	}
}

func TestScoreLanguageMismatch(t *testing.T) {
	c := Case{
		ID: "l", Lang: "en", Question: "What were sales in December?",
		Expect: Expect{Kind: KindContains, Contains: []string{"3"}},
	}
	reply := "Total penjualan pada bulan Desember adalah 3 miliar, dan ini adalah angka yang tertinggi dalam data kami."
	failures := Score(c, reply, nil)
	found := false
	for _, f := range failures {
		if strings.Contains(f, "replied in") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a language failure, got %v", failures)
	}
}

func TestScoreEmptyReply(t *testing.T) {
	c := numericCase(1, 0.01)
	if f := Score(c, "   ", nil); len(f) != 1 || f[0] != "empty reply" {
		t.Errorf("expected a single 'empty reply' failure, got %v", f)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"Total sales for December 2024 were 3.86 billion, and that is the highest month in your data.": "en",
		"Total penjualan pada bulan Desember 2024 adalah 3,86 miliar dan ini yang tertinggi.":          "id",
		"Rp 3.863.405.700": "",
	}
	for text, want := range cases {
		if got := DetectLanguage(text); got != want {
			t.Errorf("DetectLanguage(%.40q) = %q, want %q", text, got, want)
		}
	}
}

// no_figure is what a question with no answer asserts. Enumerating forbidden
// phrases cannot work there: the point is that the agent must not produce a
// number, and which number it would invent is unknowable.
func TestScoreNoFigure(t *testing.T) {
	c := Case{
		ID: "empty-result", Lang: "en",
		Expect: Expect{Kind: KindContains, NoFigure: true, MustCall: []string{"run_sql"}},
	}
	calls := []ToolInvocation{{Name: "run_sql"}}

	honest := "No sales were recorded for March 2025 — the data covers July to December 2024."
	if got := Score(c, honest, calls); len(got) != 0 {
		t.Errorf("honest empty-result answer failed: %v", got)
	}

	invented := "Total Sales for March 2025: IDR 1,488,000"
	got := Score(c, invented, calls)
	if len(got) == 0 {
		t.Fatal("an invented figure passed a no_figure case")
	}
	if !strings.Contains(got[0], "states a figure") {
		t.Errorf("failure = %q, want it to name the stated figure", got[0])
	}
}
