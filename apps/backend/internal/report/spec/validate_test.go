package spec

import (
	"strings"
	"testing"
)

// A size is one of four names or it is refused in the turn, where the model
// can still fix it (T-G11). The builder resolves an unknown name to portrait
// so it is total; this is the gate that means it never sees one.
func TestSocialSizeIsRefusedUnlessItNamesAFrame(t *testing.T) {
	for _, size := range []string{"", "portrait", "square", "story", "landscape"} {
		d := &Document{Format: "carousel", Title: "T", Social: &Social{Size: size},
			Content: Content{Sections: []Section{{Type: SectionHero, Title: "Diskon 20%"}}}}
		d.Normalize()
		if err := d.Validate(); err != nil {
			t.Errorf("size %q was refused: %v", size, err)
		}
	}
	for _, size := range []string{"vertical", "1080x1350", "reel", "4:5"} {
		d := &Document{Format: "carousel", Title: "T", Social: &Social{Size: size},
			Content: Content{Sections: []Section{{Type: SectionHero, Title: "Diskon 20%"}}}}
		d.Normalize()
		err := d.Validate()
		if err == nil {
			t.Errorf("size %q was accepted", size)
			continue
		}
		// The refusal names the four, because a model that guessed once will
		// guess again unless it is told the set.
		for _, want := range []string{"portrait", "square", "story", "landscape"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not offer %q", err, want)
			}
		}
	}
}

// Normalize lowercases and trims a size, so "Story" and " square " are the
// names they obviously mean rather than refusals.
func TestSocialSizeIsNormalized(t *testing.T) {
	d := &Document{Format: "carousel", Title: "T", Social: &Social{Size: " Story "},
		Content: Content{Sections: []Section{{Type: SectionHero, Title: "Diskon 20%"}}}}
	d.Normalize()
	if d.Social.Size != SizeStory {
		t.Errorf("size = %q, want %q", d.Social.Size, SizeStory)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// A promotion is a carousel and not a record (T-G11).
//
// The gate exists to keep an invoice out of a medium nobody can scan. An
// announcement is neither an invoice nor an analysis, so a hero satisfies it —
// otherwise the commonest social post there is would have to carry a KPI card
// the user never asked for.
func TestAnAnnouncementIsAllowedAsACarousel(t *testing.T) {
	promo := &Document{Format: "carousel", Title: "Promo",
		Social: &Social{Caption: "Diskon 20% sampai Minggu."},
		Content: Content{Sections: []Section{
			{Type: SectionHero, Subtitle: "PROMO", Title: "Diskon 20%", Text: "Semua kopi susu."},
		}}}
	promo.Normalize()
	if err := promo.Validate(); err != nil {
		t.Errorf("a one-hero promo was refused as a carousel: %v", err)
	}

	// And the test it widens still bites: a record has no hero, and a
	// carousel of one is still refused with the sentence that names the fix.
	invoice := &Document{Format: "carousel", Title: "Invoice",
		Content: Content{Sections: []Section{
			{Type: SectionKeyValue, Items: []Item{{K: "Invoice", V: "INV-1"}}},
			{Type: SectionTable, Columns: []Column{{Label: "Item"}}, Rows: [][]Cell{{{V: "Kopi"}}}},
		}}}
	invoice.Normalize()
	err := invoice.Validate()
	if err == nil {
		t.Fatal("an invoice was accepted as a carousel")
	}
	if !strings.Contains(err.Error(), "hero") || !strings.Contains(err.Error(), "announcement") {
		t.Errorf("the refusal does not offer the announcement route: %v", err)
	}

	// The video keeps the narrower test: nothing here widened it.
	video := &Document{Format: "mp4", Title: "Promo",
		Content: Content{Sections: []Section{{Type: SectionHero, Title: "Diskon 20%"}}}}
	video.Normalize()
	if err := video.Validate(); err == nil {
		t.Error("a one-hero video was accepted; T-G11 does not widen the video gate")
	}
}
