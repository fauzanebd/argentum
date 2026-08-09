package spec

import (
	"strings"
	"testing"
)

// The mp4 rules live in Validate rather than in the video renderer, because a
// refusal that arrives after a job has been queued is a refusal the model can
// no longer repair and the user has already been promised a file.

// TestMP4IsAValidFormat is the base case, and it is worth having on its own:
// the format list in Validate is the one place a new format is admitted, and
// every other check below only runs once it is.
func TestMP4IsAValidFormat(t *testing.T) {
	d := &Document{Format: "mp4", Content: Content{Sections: []Section{
		{Type: SectionHeading, Text: "Monthly review", Level: 1},
		kpiRow(),
		{Type: SectionParagraph, Text: analysis},
	}}}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestMP4RefusesARecord is the decision the format turns on. A video moves at
// its own pace and cannot be scanned, so an invoice as a video is a worse
// invoice — the reader cannot even find the total.
func TestMP4RefusesARecord(t *testing.T) {
	d := &Document{Format: "mp4", Content: Content{Sections: []Section{
		{Type: SectionKeyValue, Items: []Item{{Label: "Invoice", Value: &Cell{V: "INV-1042"}}}},
		{Type: SectionTable, Columns: []Column{{Label: "Item"}}, Rows: [][]Cell{{{V: "Consulting"}}}},
	}}}
	err := d.Validate()
	if err == nil {
		t.Fatal("an invoice was accepted as a video")
	}
	// The message has to name the way out, because the model reads it and
	// retries inside the same turn.
	for _, want := range []string{"kpi_row", "chart", "pdf"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// TestMP4AcceptsWhateverCheckNarrativeCallsAnAnalysis pins the two checks to
// one predicate.
//
// `CheckNarrative` refuses an analytical report with no prose; this refuses a
// non-analytical document as a video. They are opposite judgements about the
// same question, and if they ever disagreed about which documents are making
// an argument, a report could be simultaneously too analytical to render and
// not analytical enough to explain itself.
func TestMP4AcceptsWhateverCheckNarrativeCallsAnAnalysis(t *testing.T) {
	cases := []struct {
		name     string
		sections []Section
	}{
		{"a kpi row", []Section{kpiRow(), {Type: SectionParagraph, Text: analysis}}},
		{"a chart", []Section{barChart(), {Type: SectionParagraph, Text: analysis}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Document{Format: "mp4", Content: Content{Sections: tc.sections}}
			if !Analytical(d) {
				t.Fatal("the fixture is not analytical; the test proves nothing")
			}
			if err := d.Validate(); err != nil {
				t.Fatalf("Validate refused a document CheckNarrative would police: %v", err)
			}
		})
	}
}

// TestMP4RequiresContent keeps the format from being the one that accepts an
// empty document.
func TestMP4RequiresContent(t *testing.T) {
	d := &Document{Format: "mp4"}
	if err := d.Validate(); err == nil {
		t.Fatal("an empty document was accepted as a video")
	}
}

// TestTheFormatListNamesMP4 is about the error, not the parser: a model that
// sent `"video"` reads this string and picks again, so a format missing from
// it is a repair instruction that points the wrong way.
func TestTheFormatListNamesMP4(t *testing.T) {
	d := &Document{Format: "video"}
	err := d.Validate()
	if err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if !strings.Contains(err.Error(), "mp4") {
		t.Fatalf("the format list omits mp4: %v", err)
	}
}
