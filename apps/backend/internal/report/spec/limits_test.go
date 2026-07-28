package spec

import (
	"errors"
	"strings"
	"testing"
)

// tableOf builds a table with n rows of c columns, for the row-count tests.
func tableOf(rows, cols int) *Table {
	t := &Table{Columns: make([]Column, cols), Rows: make([][]Cell, rows)}
	for i := range t.Columns {
		t.Columns[i] = Column{Label: "c"}
	}
	for i := range t.Rows {
		t.Rows[i] = make([]Cell, cols)
	}
	return t
}

func TestCheckLimitsAcceptsAnOrdinaryDocument(t *testing.T) {
	d := &Document{Format: "pdf", Content: Content{Table: tableOf(100, 6)}}
	if err := CheckLimits(d, DefaultLimits); err != nil {
		t.Fatalf("CheckLimits: %v", err)
	}
}

// The row cap is a document total, not a per-table one. A caller who splits
// half a million rows across fifty sections has not sent a smaller document,
// and a per-table check is the obvious implementation that misses it.
func TestRowCapIsAcrossTheWholeDocument(t *testing.T) {
	d := &Document{Format: "pdf"}
	for range 5 {
		d.Content.Sections = append(d.Content.Sections, Section{
			Type:    SectionTable,
			Columns: []Column{{Label: "c"}},
			Rows:    make([][]Cell, 30),
		})
	}
	lim := Limits{MaxRows: 100}
	if err := CheckLimits(d, lim); err == nil {
		t.Fatal("150 rows across five sections passed a 100-row cap")
	}

	// And four sections of the same size do not, or the check is off by a
	// table rather than counting.
	d.Content.Sections = d.Content.Sections[:3]
	if err := CheckLimits(d, lim); err != nil {
		t.Fatalf("90 rows failed a 100-row cap: %v", err)
	}
}

func TestCheckLimitsNamesTheOffendingField(t *testing.T) {
	cases := []struct {
		name      string
		doc       *Document
		lim       Limits
		wantParam string
	}{
		{
			name:      "rows",
			doc:       &Document{Format: "csv", Content: Content{Table: tableOf(200, 2)}},
			lim:       Limits{MaxRows: 100},
			wantParam: "content.table.rows",
		},
		{
			name:      "columns",
			doc:       &Document{Format: "csv", Content: Content{Table: tableOf(1, 50)}},
			lim:       Limits{MaxCols: 40},
			wantParam: "content.table.columns",
		},
		{
			name: "cell string",
			doc: &Document{Format: "csv", Content: Content{Table: &Table{
				Columns: []Column{{Label: "c"}},
				Rows:    [][]Cell{{{V: strings.Repeat("x", 100)}}},
			}}},
			lim:       Limits{MaxStringLen: 10},
			wantParam: "content.table.rows",
		},
		{
			name: "chart points",
			doc: &Document{Format: "pdf", Content: Content{Sections: []Section{{
				Type:  SectionChart,
				Chart: &Chart{Type: ChartLine, Series: []Series{{Values: make([]float64, 500)}}},
			}}}},
			lim:       Limits{MaxChartPoints: 100},
			wantParam: "content.sections[0].chart.series",
		},
		{
			name:      "sections",
			doc:       &Document{Format: "pdf", Content: Content{Sections: make([]Section, 50)}},
			lim:       Limits{MaxSections: 10},
			wantParam: "content.sections",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckLimits(tc.doc, tc.lim)
			if err == nil {
				t.Fatal("no error")
			}
			var le *LimitError
			if !errors.As(err, &le) {
				t.Fatalf("error is %T, want *LimitError — a handler cannot name the field without it", err)
			}
			if le.Param != tc.wantParam {
				t.Errorf("Param = %q, want %q", le.Param, tc.wantParam)
			}
			if le.Limit == 0 || le.Got <= le.Limit {
				t.Errorf("Limit=%d Got=%d; the numbers should say how far over the caller was", le.Limit, le.Got)
			}
		})
	}
}

// A zero limit must not mean "unlimited". A forgotten config value would
// otherwise turn the caps off entirely, which is the exact failure they exist
// to prevent — and it would do so silently.
func TestZeroLimitsFallBackToTheDefaults(t *testing.T) {
	got := Limits{}.Normalize()
	if got != DefaultLimits {
		t.Errorf("Normalize() = %+v, want %+v", got, DefaultLimits)
	}

	d := &Document{Format: "csv", Content: Content{Table: tableOf(DefaultLimits.MaxRows+1, 2)}}
	if err := CheckLimits(d, Limits{}); err == nil {
		t.Fatal("a zero Limits accepted a document over the default row cap")
	}
}

// A number is bounded by its own rendering; only a string can be arbitrarily
// long. Checking numeric cells would be wasted work on the common path.
func TestNumericCellsAreNotStringChecked(t *testing.T) {
	d := &Document{Format: "csv", Content: Content{Table: &Table{
		Columns: []Column{{Label: "n"}},
		Rows:    [][]Cell{{{V: 3863405700}}},
	}}}
	if err := CheckLimits(d, Limits{MaxStringLen: 1}); err != nil {
		t.Fatalf("a numeric cell tripped the string cap: %v", err)
	}
}

func TestTotalRowsCountsEveryShape(t *testing.T) {
	d := &Document{
		Format: "xlsx",
		Content: Content{
			Table:  tableOf(3, 1),
			Sheets: []Sheet{{Rows: make([][]Cell, 4)}},
			Sections: []Section{
				{Type: SectionTable, Rows: make([][]Cell, 5)},
				{Type: SectionParagraph, Text: "no rows here"},
			},
		},
	}
	if got := TotalRows(d); got != 12 {
		t.Errorf("TotalRows = %d, want 12", got)
	}
}
