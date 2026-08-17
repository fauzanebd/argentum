package spec

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

func result(cols []string, rows ...map[string]any) *db.QueryResult {
	return &db.QueryResult{Columns: cols, Rows: rows, Count: len(rows)}
}

func TestProjectWideForm(t *testing.T) {
	p := &Panel{ID: "p", Viz: VizBar, Map: Mapping{Label: "month", Series: []string{"revenue", "cost"}}}
	res := result([]string{"month", "revenue", "cost"},
		map[string]any{"month": "Jan", "revenue": int64(100), "cost": 40.5},
		map[string]any{"month": "Feb", "revenue": int64(120), "cost": nil},
	)
	got, err := Project(p, res)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if fmt.Sprint(got.Labels) != "[Jan Feb]" {
		t.Errorf("labels = %v", got.Labels)
	}
	if len(got.Series) != 2 || got.Series[0].Name != "revenue" {
		t.Fatalf("series = %+v", got.Series)
	}
	if *got.Series[0].Points[1] != 120 {
		t.Errorf("revenue Feb = %v, want 120", *got.Series[0].Points[1])
	}
	// The point that matters: a NULL cost is a gap, not a zero. A bar drawn at
	// the axis is a claim the warehouse did not make.
	if got.Series[1].Points[1] != nil {
		t.Errorf("a NULL must stay nil, got %v", *got.Series[1].Points[1])
	}
}

func TestProjectLongFormPivotsAndKeepsGapsNil(t *testing.T) {
	p := &Panel{ID: "p", Viz: VizLine, Map: Mapping{Label: "month", SeriesBy: "channel", Value: "revenue"}}
	res := result([]string{"month", "channel", "revenue"},
		map[string]any{"month": "Jan", "channel": "web", "revenue": 10.0},
		map[string]any{"month": "Jan", "channel": "store", "revenue": 5.0},
		map[string]any{"month": "Feb", "channel": "web", "revenue": 12.0},
		// No February store row at all — the pivot must leave a hole.
	)
	got, err := Project(p, res)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if fmt.Sprint(got.Labels) != "[Jan Feb]" {
		t.Fatalf("labels = %v", got.Labels)
	}
	byName := map[string][]*float64{}
	for _, s := range got.Series {
		byName[s.Name] = s.Points
	}
	if len(byName) != 2 {
		t.Fatalf("series = %+v", got.Series)
	}
	if len(byName["store"]) != 2 {
		t.Fatalf("store has %d points, want one per label", len(byName["store"]))
	}
	if byName["store"][1] != nil {
		t.Errorf("a month with no row for a series must be nil, got %v", *byName["store"][1])
	}
	if got.SeriesTruncated {
		t.Error("two series is not a truncation")
	}
}

// Nine series is one more than the palette has colours, and the ninth would draw
// in the first one's red. The largest eight survive and the panel says so.
func TestProjectCapsSeriesAtThePaletteLength(t *testing.T) {
	p := &Panel{ID: "p", Viz: VizLine, Map: Mapping{Label: "month", SeriesBy: "channel", Value: "revenue"}}
	var rows []map[string]any
	for i := range 9 {
		rows = append(rows, map[string]any{"month": "Jan", "channel": fmt.Sprintf("c%d", i), "revenue": float64(i)})
	}
	got, err := Project(p, result([]string{"month", "channel", "revenue"}, rows...))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(got.Series) != SeriesCap {
		t.Fatalf("series = %d, want %d", len(got.Series), SeriesCap)
	}
	if !got.SeriesTruncated {
		t.Error("SeriesTruncated must say a series was dropped")
	}
	// The dropped one is the smallest, not whichever arrived last.
	for _, s := range got.Series {
		if s.Name == "c0" {
			t.Error("the smallest series should be the one dropped")
		}
	}
}

func TestProjectKPIKeepsNoRowsDistinctFromZero(t *testing.T) {
	p := &Panel{ID: "k", Viz: VizKPI, Map: Mapping{Value: "total", DeltaValue: "prev"}}

	empty, err := Project(p, result([]string{"total", "prev"}))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if empty.Value != nil {
		t.Errorf("no rows must leave the value unset, got %v", *empty.Value)
	}
	if empty.Note == "" {
		t.Error("no rows must carry a note the caller can read out")
	}

	got, err := Project(p, result([]string{"total", "prev"},
		map[string]any{"total": 150.0, "prev": 100.0}))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if *got.Value != 150 || *got.Comparison != 100 || *got.Delta != 50 || *got.DeltaPct != 50 {
		t.Errorf("kpi = %v %v %v %v", *got.Value, *got.Comparison, *got.Delta, *got.DeltaPct)
	}
}

// The new failure class this spec introduces: the author states column roles,
// so a rename makes a panel that used to work name a column that is gone.
func TestProjectNamesTheColumnsThatWouldHaveWorked(t *testing.T) {
	p := &Panel{ID: "p", Viz: VizBar, Map: Mapping{Label: "month", Series: []string{"turnover"}}}
	_, err := Project(p, result([]string{"month", "revenue"}, map[string]any{"month": "Jan", "revenue": 1.0}))
	if err == nil {
		t.Fatal("a mapping naming a missing column must fail")
	}
	if !strings.Contains(err.Error(), "turnover") || !strings.Contains(err.Error(), "revenue") {
		t.Errorf("the error must name what was asked for and what exists, got %q", err)
	}
}

func TestProjectTablePassesTheResultThrough(t *testing.T) {
	p := &Panel{ID: "t", Viz: VizTable}
	res := result([]string{"a", "b"}, map[string]any{"a": 1, "b": "x"})
	res.Truncated = true
	got, err := Project(p, res)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(got.Rows) != 1 || fmt.Sprint(got.Columns) != "[a b]" {
		t.Errorf("table = %+v", got)
	}
	if !got.Truncated {
		t.Error("Truncated must survive projection — it says there is more data")
	}
}

// A table declaring `fmt: currency` printed `20727672550.00` in the browser
// (2026-08-17) while the chart beside it wrote the same figure grouped. The
// browser's formatter only applies a Fmt to a number, and a Postgres `numeric`
// arrives as a string — so the coercion every other viz does through cell() has
// to happen here too.
func TestProjectTableGivesTheBrowserNumbersItCanFormat(t *testing.T) {
	p := &Panel{ID: "t", Viz: VizTable, Fmt: FmtCurrency}
	res := result([]string{"revenue", "share", "n"},
		map[string]any{"revenue": "20727672550.00", "share": []byte("-0.5"), "n": int64(3)},
	)
	got, err := Project(p, res)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if v, ok := got.Rows[0]["revenue"].(float64); !ok || v != 20727672550 {
		t.Errorf("revenue = %#v, want the number 20727672550", got.Rows[0]["revenue"])
	}
	if v, ok := got.Rows[0]["share"].(float64); !ok || v != -0.5 {
		t.Errorf("share = %#v, want the number -0.5", got.Rows[0]["share"])
	}
	if got.Rows[0]["n"] != int64(3) {
		t.Errorf("an int64 must travel as it arrived, got %#v", got.Rows[0]["n"])
	}
}

// The narrowing that keeps the fix from being worse than the defect: a table is
// the one panel that draws columns nobody mapped, and half of them are codes.
func TestProjectTableLeavesIdentifiersAlone(t *testing.T) {
	p := &Panel{ID: "t", Viz: VizTable}
	res := result([]string{"phone", "sku", "intl", "sci", "when", "note"},
		map[string]any{
			"phone": "081234567890", // a leading zero is a code, not a quantity
			"sku":   "00123",        // padded, and the padding is the meaning
			"intl":  "+6281234567",  // a sign in the middle of a phone number
			"sci":   "1e6",          // not how a driver writes a numeric
			"when":  "2024-11-01",   // a date is not an arithmetic expression
			"note":  " 42 ",         // whitespace means somebody typed it
		},
	)
	got, err := Project(p, res)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	for _, col := range res.Columns {
		if _, coerced := got.Rows[0][col].(float64); coerced {
			t.Errorf("%s was turned into a number: %#v", col, got.Rows[0][col])
		}
	}
}

// Drivers return numbers as whatever their protocol carried; a chart must not
// care which.
func TestProjectCoercesTheShapesDriversReturn(t *testing.T) {
	p := &Panel{ID: "p", Viz: VizPie, Map: Mapping{Label: "channel", Value: "v"}}
	got, err := Project(p, result([]string{"channel", "v"},
		map[string]any{"channel": "web", "v": []byte("12.5")},
		map[string]any{"channel": "store", "v": "3"},
		map[string]any{"channel": "app", "v": int32(7)},
	))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	pts := got.Series[0].Points
	if *pts[0] != 12.5 || *pts[1] != 3 || *pts[2] != 7 {
		t.Errorf("points = %v %v %v", *pts[0], *pts[1], *pts[2])
	}

	_, err = Project(p, result([]string{"channel", "v"}, map[string]any{"channel": "web", "v": "twelve"}))
	if err == nil {
		t.Error("a non-numeric measure must be an error, not a zero")
	}
}
