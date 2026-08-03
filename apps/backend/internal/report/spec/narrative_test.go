package spec

import (
	"strings"
	"testing"
)

// analysis is prose long enough to clear MinNarrativeChars, so a test that
// means "this report was written" does not read as a character count.
const analysis = "Revenue closed June at Rp 4,01 billion, 3,9% above May and the third " +
	"consecutive month of growth. The whole of the gain came from the North region, where " +
	"the two enterprise accounts renewed early; every other region was flat. Refunds rose " +
	"in the same period and are the one figure worth watching next month."

func kpiRow() Section {
	v := 12.5
	return Section{Type: SectionKPIRow, Items: []Item{
		{Label: "Revenue", Value: &Cell{V: 4012118800, Fmt: "currency"}, DeltaPct: &v},
	}}
}

func barChart() Section {
	return Section{Type: SectionChart, Chart: &Chart{
		Type:   ChartBar,
		Labels: []string{"May", "Jun"},
		Series: []Series{{Name: "Revenue", Values: []float64{3863405700, 4012118800}}},
	}}
}

// The check fires on the shape it was written for: figures, no reading of them.
func TestCheckNarrativeRefusesFiguresWithoutInterpretation(t *testing.T) {
	d := &Document{Format: "pdf", Content: Content{Sections: []Section{
		{Type: SectionCover, Text: "June review"},
		kpiRow(),
		barChart(),
		{Type: SectionTable, Columns: []Column{{Label: "Region"}}, Rows: [][]Cell{{{V: "North"}}}},
	}}}

	err := CheckNarrative(d)
	if err == nil {
		t.Fatal("a report of pure figures was accepted")
	}
	// The message is a repair instruction, not a verdict: the model reads it and
	// fixes the spec inside the same turn, so it has to name the section types.
	for _, want := range []string{"paragraph", "callout", "Executive summary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not tell the model to add %q: %v", want, err)
		}
	}
}

func TestCheckNarrativeAcceptsAWrittenReport(t *testing.T) {
	d := &Document{Format: "pdf", Content: Content{Sections: []Section{
		{Type: SectionCover, Text: "June review"},
		{Type: SectionHeading, Text: "Executive summary", Level: 1},
		{Type: SectionParagraph, Text: analysis},
		kpiRow(),
	}}}
	if err := CheckNarrative(d); err != nil {
		t.Fatalf("a written report was refused: %v", err)
	}
}

// A callout carries interpretation as readily as a paragraph does, and a short
// report whose whole finding is one warning box has still been written.
func TestCheckNarrativeCountsCallouts(t *testing.T) {
	d := &Document{Format: "pptx", Content: Content{Sections: []Section{
		barChart(),
		{Type: SectionCallout, Tone: ToneWarn, Title: "Refunds are the exception", Text: analysis},
	}}}
	if err := CheckNarrative(d); err != nil {
		t.Fatalf("a callout did not count as interpretation: %v", err)
	}
}

// The tool is generic-purpose. An invoice, an agreement and a T&C are records,
// not arguments, and prose explaining an invoice's own totals would be a worse
// invoice — so the check waits for a KPI row or a chart before it fires.
func TestCheckNarrativeSkipsDocumentsThatAreNotAnalyses(t *testing.T) {
	cases := []struct {
		name string
		doc  *Document
	}{
		{"invoice", &Document{Format: "pdf", Content: Content{Sections: []Section{
			{Type: SectionKeyValue, Items: []Item{{K: "Invoice", V: "INV-2026-041"}}},
			{Type: SectionTable, Columns: []Column{{Label: "Item"}}, Rows: [][]Cell{{{V: "Retainer"}}}},
		}}}},
		{"data export", &Document{Format: "pdf", Content: Content{Sections: []Section{
			{Type: SectionTable, Columns: []Column{{Label: "Month"}}, Rows: [][]Cell{{{V: "2026-06"}}}},
		}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckNarrative(tc.doc); err != nil {
				t.Fatalf("a %s was asked to interpret itself: %v", tc.name, err)
			}
		})
	}
}

// A spreadsheet has no prose to hold. Cells are data, and a paragraph in one is
// a row nobody can sum.
func TestCheckNarrativeSkipsSpreadsheetFormats(t *testing.T) {
	for _, format := range []string{"xlsx", "csv"} {
		d := &Document{Format: format, Content: Content{Sections: []Section{kpiRow()}}}
		if err := CheckNarrative(d); err != nil {
			t.Errorf("%s was held to the narrative check: %v", format, err)
		}
	}
}

// A heading is a label, a footnote is a source line and a caption says where the
// numbers came from. None of them says what the numbers mean, and counting them
// would let a thorough methodology note stand in for an analysis.
func TestNarrativeCharsIgnoresLabelsAndSources(t *testing.T) {
	d := &Document{Format: "pdf", Content: Content{Sections: []Section{
		{Type: SectionHeading, Text: analysis},
		{Type: SectionFootnote, Text: analysis},
		{Type: SectionTable, Caption: analysis, Columns: []Column{{Label: "Region"}}},
		kpiRow(),
	}}}
	if n := NarrativeChars(d); n != 0 {
		t.Errorf("NarrativeChars = %d, want 0: headings, footnotes and captions are not analysis", n)
	}
	if err := CheckNarrative(d); err == nil {
		t.Fatal("a report whose only prose was labels and sources was accepted")
	}
}

// One word is a caption, not a reading. The floor exists so the check is not
// satisfied by a token the model never thinks about again.
func TestCheckNarrativeRefusesATokenParagraph(t *testing.T) {
	d := &Document{Format: "pdf", Content: Content{Sections: []Section{
		kpiRow(),
		{Type: SectionParagraph, Text: "Revenue grew."},
	}}}
	if err := CheckNarrative(d); err == nil {
		t.Fatal("a two-word paragraph satisfied the narrative check")
	}
}
