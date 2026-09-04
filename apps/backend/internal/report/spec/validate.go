package spec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// knownSections is the closed set. Listing it in the error message matters
// more than it looks: the model reads the tool error and retries in the same
// turn, so an error that names the alternatives is a repair instruction.
var knownSections = []string{
	SectionCover, SectionHeading, SectionParagraph, SectionKPIRow,
	SectionTable, SectionChart, SectionCallout, SectionKeyValue,
	SectionFootnote, SectionPageBreak, SectionSpacer,
}

// Normalize lower-cases and trims the free-text discriminators before
// validation, so `"Type": "Table "` from a model having an off day is not a
// hard error.
func (d *Document) Normalize() {
	d.Format = strings.ToLower(strings.TrimSpace(d.Format))
	d.Locale = strings.ToLower(strings.TrimSpace(d.Locale))
	d.Currency = strings.ToUpper(strings.TrimSpace(d.Currency))
	if d.Social != nil {
		// "#promo", " promo " and "promo" are one hashtag. The renderer writes
		// the "#" once, on the way out, so it is never doubled.
		d.Social.Caption = strings.TrimSpace(d.Social.Caption)
		tags := d.Social.Hashtags[:0]
		for _, h := range d.Social.Hashtags {
			h = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(h), "#"))
			if h != "" {
				tags = append(tags, h)
			}
		}
		d.Social.Hashtags = tags
	}
	for i := range d.Content.Sections {
		s := &d.Content.Sections[i]
		s.Type = strings.ToLower(strings.TrimSpace(s.Type))
		s.Tone = strings.ToLower(strings.TrimSpace(s.Tone))
		if s.Chart != nil {
			// "Grouped Bar" and "grouped-bar" are both things a model writes
			// when the schema says grouped_bar, and neither is a reason to
			// refuse a chart.
			t := strings.ToLower(strings.TrimSpace(s.Chart.Type))
			s.Chart.Type = strings.ReplaceAll(strings.ReplaceAll(t, " ", "_"), "-", "_")
		}
	}
}

// Validate gives early, actionable errors before a renderer is reached.
func (d *Document) Validate() error {
	switch d.Format {
	case "pdf", "xlsx", "csv", "pptx", "mp4", "carousel":
	default:
		return fmt.Errorf("format must be one of pdf|xlsx|csv|pptx|mp4|carousel (got %q)", d.Format)
	}

	if d.Social != nil {
		if n := utf8.RuneCountInString(d.Social.Caption); n > MaxCaptionChars {
			return fmt.Errorf("social.caption is %d characters and the cap is %d — shorten it", n, MaxCaptionChars)
		}
		if n := len(d.Social.Hashtags); n > MaxHashtags {
			return fmt.Errorf("social.hashtags has %d entries and the cap is %d — keep the ones that matter", n, MaxHashtags)
		}
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
	case "pdf", "pptx":
		// A deck reads the same content tree as a document: the sections are
		// the same sections, projected onto slides instead of onto pages. That
		// is the whole point of the format — nothing about a report has to be
		// authored twice — so there is nothing extra to require here.
		if len(d.Content.Sections) == 0 && d.Content.Table == nil {
			return fmt.Errorf("%s requires content.sections or content.table", d.Format)
		}
	case "mp4", "carousel":
		if len(d.Content.Sections) == 0 && d.Content.Table == nil {
			return fmt.Errorf("%s requires content.sections or content.table", d.Format)
		}
		// The one format that refuses a document it could render.
		//
		// A video is watched: it moves at its own pace and the viewer cannot
		// scroll back. That is the right medium for an argument about numbers
		// and the wrong one for a record somebody needs to read a line of — an
		// invoice as a video is a worse invoice, and the reader cannot even
		// find the total. `Analytical` is the same predicate `CheckNarrative`
		// uses for the mirror-image judgement, so the two cannot disagree about
		// which documents are making an argument.
		if !Analytical(d) {
			medium := "video"
			if d.Format == "carousel" {
				medium = "carousel"
			}
			return fmt.Errorf("%s is for reports that make an argument about data, and this document has neither a "+
				"\"kpi_row\" nor a \"chart\" in it: a record — an invoice, an agreement, a data export — is worse as a "+
				"%s than as a PDF, because the reader cannot scan it or find one line. Render this as \"pdf\", or add "+
				"the figures the %s would be about", d.Format, medium, medium)
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
		if s.Chart == nil {
			return fmt.Errorf("chart requires a chart object: {\"type\": \"chart\", \"chart\": {\"type\": \"%s\", ...}}", ChartLine)
		}
		return s.Chart.Validate()
	case SectionPageBreak, SectionSpacer:
	case "":
		return fmt.Errorf("section requires a type (one of %s)", strings.Join(knownSections, "|"))
	default:
		return fmt.Errorf("unknown section type %q (want one of %s)", s.Type, strings.Join(knownSections, "|"))
	}
	return nil
}

// Validate checks the chart payload before a renderer is reached.
//
// It is stricter than the rest of this file, and deliberately so. Everywhere
// else a malformed field degrades — a cell that will not parse prints as text,
// a KPI written with the wrong key names still renders. A chart cannot degrade:
// a series with three values against five labels is not a chart missing two
// points, it is a chart whose points are against the wrong labels, and it will
// draw without complaint. The reader has no way to see that it is wrong.
func (c *Chart) Validate() error {
	found := false
	for _, t := range ChartTypes {
		if c.Type == t {
			found = true
			break
		}
	}
	if !found {
		if c.Type == "" {
			return fmt.Errorf("chart requires a type (one of %s)", strings.Join(ChartTypes, "|"))
		}
		return fmt.Errorf("unknown chart type %q (want one of %s)", c.Type, strings.Join(ChartTypes, "|"))
	}

	if len(c.Series) == 0 {
		return fmt.Errorf("%s chart requires series: [{\"name\": \"Revenue\", \"values\": [1, 2, 3]}]", c.Type)
	}
	// A sparkline is a shape, not a plot: it has no axis, so it has nothing to
	// label. Every other type puts the labels on an axis a reader reads.
	if len(c.Labels) == 0 && c.Type != ChartSparkline {
		return fmt.Errorf("%s chart requires labels, one per value", c.Type)
	}
	for i, s := range c.Series {
		if len(s.Values) == 0 {
			return fmt.Errorf("chart.series[%d] (%q) has no values", i, s.Name)
		}
		if len(c.Labels) > 0 && len(s.Values) != len(c.Labels) {
			return fmt.Errorf("chart.series[%d] (%q) has %d values against %d labels; they must be one to one",
				i, s.Name, len(s.Values), len(c.Labels))
		}
	}
	if SingleSeries(c.Type) && len(c.Series) > 1 {
		return fmt.Errorf("a %s chart draws one series (got %d); use grouped_bar to compare several",
			c.Type, len(c.Series))
	}
	if c.YAxis != nil && c.YAxis.Min != nil && c.YAxis.Max != nil && *c.YAxis.Min >= *c.YAxis.Max {
		return fmt.Errorf("chart.y_axis.min (%v) must be below max (%v)", *c.YAxis.Min, *c.YAxis.Max)
	}
	return nil
}
