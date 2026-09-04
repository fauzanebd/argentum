package spec

import (
	"strings"
	"testing"
)

// The carousel's rules in Validate (T-G6), beside the mp4's: admitted as a
// format, refused for a record, and the post's text bounded by the platform.

func TestCarouselIsAValidFormatForAnArgument(t *testing.T) {
	d := &Document{Format: "carousel", Content: Content{Sections: []Section{
		{Type: SectionHeading, Text: "Sorotan", Level: 1},
		kpiRow(),
		{Type: SectionParagraph, Text: analysis},
	}}, Social: &Social{Caption: "Agustus tumbuh 9,8%", Hashtags: []string{"promo", "#agustus"}}}
	d.Normalize()
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// "#agustus" and "promo" are stored without the "#", so the renderer
	// writes it once on the way out.
	if got := strings.Join(d.Social.Hashtags, ","); got != "promo,agustus" {
		t.Errorf("hashtags normalised to %q, want promo,agustus", got)
	}
}

// A record is refused as a carousel for the reason it is refused as a video,
// through the same predicate — and the message names the format, so the model
// is not told a carousel is a video.
func TestCarouselRefusesARecord(t *testing.T) {
	d := &Document{Format: "carousel", Content: Content{Sections: []Section{
		{Type: SectionKeyValue, Items: []Item{{Label: "Invoice", Value: &Cell{V: "INV-1042"}}}},
	}}}
	err := d.Validate()
	if err == nil {
		t.Fatal("an invoice was accepted as a carousel")
	}
	for _, want := range []string{"carousel is for reports", "kpi_row", "chart", "pdf"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "video") {
		t.Errorf("a carousel refusal talks about a video: %v", err)
	}
}

// The platform's caps are refusals, not truncations: which sentence to drop is
// the model's decision about its own argument.
func TestSocialTextIsBoundedByThePlatform(t *testing.T) {
	base := func() *Document {
		return &Document{Format: "carousel", Content: Content{Sections: []Section{kpiRow()}}}
	}

	long := base()
	long.Social = &Social{Caption: strings.Repeat("a", MaxCaptionChars+1)}
	if err := long.Validate(); err == nil || !strings.Contains(err.Error(), "social.caption") {
		t.Errorf("a %d-character caption was accepted: %v", MaxCaptionChars+1, err)
	}

	exact := base()
	exact.Social = &Social{Caption: strings.Repeat("é", MaxCaptionChars)} // runes, not bytes
	if err := exact.Validate(); err != nil {
		t.Errorf("a caption of exactly %d runes was refused: %v", MaxCaptionChars, err)
	}

	tags := base()
	tags.Social = &Social{Hashtags: make([]string, MaxHashtags+1)}
	for i := range tags.Social.Hashtags {
		tags.Social.Hashtags[i] = "t"
	}
	if err := tags.Validate(); err == nil || !strings.Contains(err.Error(), "social.hashtags") {
		t.Errorf("%d hashtags were accepted: %v", MaxHashtags+1, err)
	}

	// Empty and "#"-only hashtags are dropped by Normalize rather than counted.
	blanks := base()
	blanks.Social = &Social{Hashtags: []string{"", "#", "  ", "#real"}}
	blanks.Normalize()
	if got := strings.Join(blanks.Social.Hashtags, ","); got != "real" {
		t.Errorf("blank hashtags survived normalisation: %q", got)
	}
}
