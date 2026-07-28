package measure

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// A width of zero means the embedded faces did not load, and every column in
// every document would be laid out against the crude fallback estimate without
// anything failing. That is the one measurement failure that is invisible
// downstream, so it is asserted here rather than inferred from a rendered file.
func TestWidthUsesTheEmbeddedFaces(t *testing.T) {
	if _, err := measurer(); err != nil {
		t.Fatalf("measurer: %v", err)
	}
	w := Width("Pendapatan", theme.FontBody, Regular, theme.TypeScale.Body)
	if w <= 0 {
		t.Fatalf("width of a non-empty string is %v", w)
	}
	// Bold is a separate face rather than a synthesised emboldening, so it
	// measures differently from the upright — not necessarily wider. Space
	// Grotesk Bold is in fact a shade narrower than its Regular at this string
	// (20.74mm against 20.79mm), which is a fact about the family and not a
	// registration failure. Asserting "wider" would have encoded a guess about
	// the face as a property of the measurer.
	if b := Width("Pendapatan", theme.FontBody, Bold, theme.TypeScale.Body); b == w {
		t.Errorf("bold and regular both measure %v — the bold face did not register", w)
	}
	if e := Width("", theme.FontBody, Regular, theme.TypeScale.Body); e != 0 {
		t.Errorf("empty string measures %v, want 0", e)
	}
}

func TestWrapFillsLinesToWidth(t *testing.T) {
	const text = "Pendapatan tahun berjalan mencapai angka tertinggi sejak kuartal kedua tahun lalu"
	width := 40.0
	lines := Wrap(text, theme.FontBody, Regular, theme.TypeScale.Body, width)
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want the text to wrap", len(lines))
	}
	for i, ln := range lines {
		if w := Width(ln, theme.FontBody, Regular, theme.TypeScale.Body); w > width {
			t.Errorf("line %d is %vmm wide, over the %vmm measure: %q", i, w, width, ln)
		}
	}
	if joined := strings.Join(lines, " "); joined != text {
		t.Errorf("wrapping lost or added words:\n got %q\nwant %q", joined, text)
	}
}

func TestTruncateMarksWhatItCut(t *testing.T) {
	const text = "Kenaikan terkonsentrasi pada kanal marketplace di bulan September dan Oktober tahun ini"
	got := Truncate(text, theme.FontBody, Regular, theme.TypeScale.Body, 40, 2)
	if got == text {
		t.Fatal("nothing was cut from a string that does not fit in two lines")
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated silently: %q", got)
	}
	if n := len(Wrap(got, theme.FontBody, Regular, theme.TypeScale.Body, 40)); n > 2 {
		t.Errorf("truncated text still wraps to %d lines", n)
	}
}

// The case Truncate cannot see: one token with no spaces, already on one line
// and already too wide. It has to be cut by characters or it draws over its
// neighbour.
func TestFitCutsAnUnbreakableToken(t *testing.T) {
	const token = "SO-2026-4100-8821-XZ"
	width := 12.0
	got := Fit(token, theme.FontBody, Regular, theme.TypeScale.Body, width, 1)
	if got == token {
		t.Fatalf("unbreakable token was left at full width")
	}
	if w := Width(got, theme.FontBody, Regular, theme.TypeScale.Body); w > width {
		t.Errorf("fitted token is %vmm wide, over the %vmm column: %q", w, width, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clipped silently: %q", got)
	}
}
