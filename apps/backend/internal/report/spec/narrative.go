package spec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MinNarrativeChars is the floor a report's prose has to clear.
//
// Two sentences. Below that nothing has been explained: "Revenue grew." is a
// caption, not an analysis, and a check that accepted it would be a check the
// model satisfies with one word and never thinks about again. It is deliberately
// a low bar — the tool description asks for a paragraph beside every figure, and
// this only catches the reports that skipped prose altogether.
const MinNarrativeChars = 200

// Analytical reports whether a document is making an argument about data.
//
// The distinction matters because `generate_document` is generic-purpose: the
// same tool produces invoices, agreements and T&Cs, and an invoice that
// explained its own totals in a paragraph would be a worse invoice. A KPI row or
// a chart is what separates the two — both exist to make a point about a number
// against some baseline, and neither appears on a document that is only a
// record. A table alone is not enough: a data export is a table.
func Analytical(d *Document) bool {
	for _, s := range d.Content.Sections {
		if s.Type == SectionKPIRow || s.Type == SectionChart {
			return true
		}
	}
	return false
}

// NarrativeChars totals the interpretation in a document.
//
// Only `paragraph` and `callout` count. A `heading` is a label, a `footnote` is
// a source and methodology line, and a table or chart `caption` says where the
// numbers came from — all of them can be long, none of them says what the
// numbers mean, and counting them would let a thorough methodology note stand in
// for an analysis that was never written.
func NarrativeChars(d *Document) int {
	n := 0
	for _, s := range d.Content.Sections {
		switch s.Type {
		case SectionParagraph:
			n += utf8.RuneCountInString(strings.TrimSpace(s.Text))
		case SectionCallout:
			n += utf8.RuneCountInString(strings.TrimSpace(s.Title))
			n += utf8.RuneCountInString(strings.TrimSpace(s.Text))
		}
	}
	return n
}

// CheckNarrative refuses an analytical report that only states figures.
//
// **Why this is an error and not a warning.** There is no warning channel back
// to the model. The tool result is either a download URL — at which point the
// document has been rendered, uploaded, metered and handed to the user, and
// nothing can be said about it any more — or an error, which the model reads and
// repairs from inside the same turn. That is the same reasoning that makes
// `Chart.Validate` strict: a defect the reader cannot see has to be caught
// before the reader gets the file.
//
// It is checked only on the agent's own path (`docgen.Input.EnforceNarrative`).
// A spec that arrived at `POST /v1/reports/render` was authored by the
// integrator, who is entitled to render a KPI sheet with no prose in it, and
// refusing one would break a contract they already have working.
func CheckNarrative(d *Document) error {
	if d.Format != "pdf" && d.Format != "pptx" {
		return nil
	}
	if !Analytical(d) {
		return nil
	}
	if n := NarrativeChars(d); n < MinNarrativeChars {
		return fmt.Errorf(
			"this report states figures without interpreting them (%d characters of prose; at least %d are required): "+
				"the reader already has these numbers, so a document that repeats them adds nothing. "+
				"Add \"paragraph\" sections that say what the figures mean — which ones moved and against what baseline, "+
				"what shape the chart is showing and when it turned, which two or three table rows matter and why — "+
				"and a \"callout\" for the one finding they must not miss. "+
				"Open with an \"Executive summary\" heading and a paragraph answering what happened, why, and what to do next. "+
				"Ground every claim in a figure you retrieved in this turn; where you do not know a cause, say the data does not show it",
			n, MinNarrativeChars)
	}
	return nil
}
