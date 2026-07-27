package spec

import (
	"fmt"
	"strings"
)

// knownSections is the closed set. Listing it in the error message matters
// more than it looks: the model reads the tool error and retries in the same
// turn, so an error that names the alternatives is a repair instruction.
var knownSections = []string{
	SectionCover, SectionHeading, SectionParagraph, SectionKPIRow,
	SectionTable, SectionCallout, SectionKeyValue, SectionFootnote,
	SectionPageBreak, SectionSpacer,
}

// Normalize lower-cases and trims the free-text discriminators before
// validation, so `"Type": "Table "` from a model having an off day is not a
// hard error.
func (d *Document) Normalize() {
	d.Format = strings.ToLower(strings.TrimSpace(d.Format))
	d.Locale = strings.ToLower(strings.TrimSpace(d.Locale))
	d.Currency = strings.ToUpper(strings.TrimSpace(d.Currency))
	for i := range d.Content.Sections {
		s := &d.Content.Sections[i]
		s.Type = strings.ToLower(strings.TrimSpace(s.Type))
		s.Tone = strings.ToLower(strings.TrimSpace(s.Tone))
	}
}

// Validate gives early, actionable errors before a renderer is reached.
func (d *Document) Validate() error {
	switch d.Format {
	case "pdf", "xlsx", "csv":
	default:
		return fmt.Errorf("format must be one of pdf|xlsx|csv (got %q)", d.Format)
	}

	switch d.Format {
	case "csv":
		if d.Content.Table == nil {
			return fmt.Errorf("csv requires content.table")
		}
		if len(d.Content.Table.Columns) == 0 {
			return fmt.Errorf("csv requires content.table.columns")
		}
	case "xlsx":
		if d.Content.Table == nil && len(d.Content.Sheets) == 0 {
			return fmt.Errorf("xlsx requires content.table or content.sheets")
		}
	case "pdf":
		if len(d.Content.Sections) == 0 && d.Content.Table == nil {
			return fmt.Errorf("pdf requires content.sections or content.table")
		}
	}

	covers := 0
	for i, s := range d.Content.Sections {
		if err := s.validate(); err != nil {
			return fmt.Errorf("content.sections[%d]: %w", i, err)
		}
		if s.Type == SectionCover {
			covers++
		}
	}
	if covers > 1 {
		return fmt.Errorf("content.sections: %d cover sections; a document has one cover", covers)
	}
	return nil
}

func (s Section) validate() error {
	switch s.Type {
	case SectionCover:
		if strings.TrimSpace(s.Text) == "" && strings.TrimSpace(s.Title) == "" {
			// The cover falls back to the document title, so this is only an
			// error when there is nothing anywhere. Checked by the renderer,
			// which is the only place that knows the document title.
			return nil
		}
	case SectionHeading:
		if strings.TrimSpace(s.Text) == "" {
			return fmt.Errorf("heading requires text")
		}
		if s.Level != 0 && s.Level != 1 && s.Level != 2 {
			return fmt.Errorf("heading level must be 1 or 2 (got %d)", s.Level)
		}
	case SectionParagraph, SectionFootnote:
		if strings.TrimSpace(s.Text) == "" {
			return fmt.Errorf("%s requires text", s.Type)
		}
	case SectionCallout:
		if strings.TrimSpace(s.Text) == "" && strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("callout requires title or text")
		}
		switch s.Tone {
		case "", ToneInfo, ToneWarn, ToneGood:
		default:
			return fmt.Errorf("callout tone must be one of info|warn|good (got %q)", s.Tone)
		}
	case SectionKeyValue:
		if len(s.Items) == 0 {
			return fmt.Errorf("key_value requires items")
		}
	case SectionKPIRow:
		if len(s.Items) == 0 {
			return fmt.Errorf("kpi_row requires items")
		}
		// Four is the width at which cards stop being readable on A4 at the
		// theme's type scale — beyond that the value text wraps and the card
		// stops being a card.
		if len(s.Items) > 4 {
			return fmt.Errorf("kpi_row takes 2-4 items (got %d); split into two rows", len(s.Items))
		}
	case SectionTable:
		if len(s.Columns) == 0 {
			return fmt.Errorf("table requires columns")
		}
	case SectionChart:
		// T-R3 owns this. Rejecting is better than dropping: a report the
		// model believes has a chart in it, silently rendered without one, is
		// a document whose narrative refers to a figure that is not there.
		return fmt.Errorf("chart sections are not supported by this renderer yet; " +
			"describe the trend in a paragraph or render the numbers as a table")
	case SectionPageBreak, SectionSpacer:
	case "":
		return fmt.Errorf("section requires a type (one of %s)", strings.Join(knownSections, "|"))
	default:
		return fmt.Errorf("unknown section type %q (want one of %s)", s.Type, strings.Join(knownSections, "|"))
	}
	return nil
}
