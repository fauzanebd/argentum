package canvas

import (
	"math"
	"testing"
)

// The refactor's own gate (T-G2): a surface is a value, so a second one with a
// different width measures differently and leaves Wide exactly where it was.
// The five golden plans in videoplan/testdata and the deck fixtures are the
// other half of that sentence — this test is the half that a byte-identical
// golden cannot express, because no golden was ever built on the other surface.
func TestASecondSurfaceMeasuresItselfAndNotWide(t *testing.T) {
	wideBefore := Wide.ContentWidth()

	narrow := Wide
	narrow.WidthMM = 190.5
	narrow.PxW = 1080

	if got := narrow.ContentWidth(); got != 190.5-2*Wide.MarginX {
		t.Errorf("narrow content width = %.4f, want %.4f", got, 190.5-2*Wide.MarginX)
	}
	if got := Wide.ContentWidth(); got != wideBefore {
		t.Errorf("Wide.ContentWidth moved from %.4f to %.4f when a copy was edited", wideBefore, got)
	}
	// A narrower measure packs fewer characters a line, so the same value takes
	// more rows on the narrow surface. This is the property every wrap decision
	// in the builders rests on.
	const address = "Meridian Logistics Pte Ltd, 8 Marina View #22-01, Asia Square Tower 2, Singapore 018961"
	if w, n := Wide.FactRowHeight(address), narrow.FactRowHeight(address); n <= w {
		t.Errorf("fact row on the narrow surface is %.2fmm, wide is %.2fmm — the narrow surface did not wrap more", n, w)
	}
}

// Portrait is the surface that is taller than wide, and the predicate has to be
// asked of the frame: the 4:5 surface's measure is wider than its body.
func TestPortraitIsTallerThanWideAndWideIsNot(t *testing.T) {
	if Wide.Portrait() {
		t.Error("Wide reports portrait")
	}
	if !Portrait.Portrait() {
		t.Error("Portrait does not report portrait")
	}
	if Portrait.ContentWidth() < Portrait.BodyHeight() {
		t.Errorf("portrait measure %.1fmm is narrower than its body %.1fmm — the comment on Portrait() is stale",
			Portrait.ContentWidth(), Portrait.BodyHeight())
	}
	if Portrait.WidthMM != Wide.HeightMM {
		t.Errorf("Portrait.WidthMM %.4f != Wide.HeightMM %.4f — the 2 px/pt construction moved", Portrait.WidthMM, Wide.HeightMM)
	}
}

// Wide's pixel size and its millimetre size are carried separately so that
// this can be asserted rather than assumed.
func TestWideIsThe1920x1080FrameAtTwoPxPerPoint(t *testing.T) {
	if got := float64(Wide.PxW) / Wide.WidthMM; math.Abs(got-PxPerMM) > 1e-9 {
		t.Errorf("Wide is %d px over %.4f mm = %.6f px/mm, want PxPerMM %.6f", Wide.PxW, Wide.WidthMM, got, PxPerMM)
	}
	if got := float64(Wide.PxH) / Wide.HeightMM; math.Abs(got-PxPerMM) > 1e-9 {
		t.Errorf("Wide is %d px over %.4f mm tall = %.6f px/mm, want PxPerMM %.6f", Wide.PxH, Wide.HeightMM, got, PxPerMM)
	}
	if math.Abs(PxPerPt-2.0) > 1e-9 {
		t.Errorf("PxPerPt = %.9f, want exactly 2 — the package comment's whole argument", PxPerPt)
	}
}

// The type scale the deck and the video have always drawn with, pinned by
// value: the refactor moved these numbers off package constants and a moved
// number is the failure mode canvas.go's own comment records.
//
// Display is 43, not the 43.5 the pre-T-G2 comment beside it claimed: 24pt at
// 1.8 is 43.2, and the half-point rounding takes that down. The comment was
// wrong for the whole life of the constant and nothing read it; this does.
func TestWideTypeScaleIsUnchanged(t *testing.T) {
	want := map[string][2]float64{
		"Display": {Wide.Type.Display, 43},
		"H1":      {Wide.Type.H1, 29},
		"H2":      {Wide.Type.H2, 23.5},
		"Body":    {Wide.Type.Body, 18},
		"Caption": {Wide.Type.Caption, 14.5},
	}
	for name, v := range want {
		if v[0] != v[1] {
			t.Errorf("Wide.Type.%s = %v, want %v", name, v[0], v[1])
		}
	}
	if Wide.ScalePt(16) != 29 {
		t.Errorf("ScalePt(16) = %v on Wide, want 29", Wide.ScalePt(16))
	}
}

// The zero value is Wide to every caller, so an Options struct written before
// surfaces existed keeps meaning what it meant.
func TestZeroSurfaceResolvesToTheFallback(t *testing.T) {
	var zero Surface
	if !zero.IsZero() {
		t.Fatal("the zero Surface does not report IsZero")
	}
	if got := zero.Or(Wide); got != Wide {
		t.Errorf("zero.Or(Wide) = %+v, want Wide", got)
	}
	if Wide.IsZero() {
		t.Error("Wide reports IsZero")
	}
	if got := Wide.Or(Surface{WidthMM: 1}); got != Wide {
		t.Error("a set surface was replaced by the fallback")
	}
}

// The four social frames, pinned by value (T-G11).
//
// A surface is margins, a type scale, safe zones and a card cap, and every one
// of those is a decision somebody argued for against a frame they looked at.
// This test is what makes a change to any of them deliberate: the numbers are
// here, so moving one is a diff a reviewer reads rather than a layout a
// tenant's audience sees first.
func TestTheSocialSurfacesArePinned(t *testing.T) {
	cases := []struct {
		name                    string
		s                       Surface
		w, h                    int
		marginTop, marginBottom float64
		bodyHeightMM, measureMM float64
		maxKPICards             int
	}{
		// Portrait is unchanged by T-G11 and is here as the control: if these
		// numbers move, the carousel's goldens move with them.
		{"portrait", Portrait, 1080, 1350, 22.0, 27.0, 154.1, 162.5, 4},
		{"square", Square, 1080, 1080, 22.0, 27.0, 106.5, 162.5, 3},
		// Story's margins are the app's chrome, not a scaled copy of
		// Portrait's: ~250px of account row above, ~340px of reply bar below.
		{"story", Story, 1080, 1920, 44.0, 60.0, 199.7, 162.5, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.s.PxW != tc.w || tc.s.PxH != tc.h {
				t.Errorf("frame = %dx%d, want %dx%d", tc.s.PxW, tc.s.PxH, tc.w, tc.h)
			}
			// The millimetre and pixel sizes have to agree, or the plan's
			// measurements describe a frame the renderer does not draw.
			if got := Px(tc.s.WidthMM); got != tc.w {
				t.Errorf("width %.3fmm is %dpx, want %d", tc.s.WidthMM, got, tc.w)
			}
			if got := Px(tc.s.HeightMM); got != tc.h {
				t.Errorf("height %.3fmm is %dpx, want %d", tc.s.HeightMM, got, tc.h)
			}
			if tc.s.MarginTop != tc.marginTop || tc.s.MarginBottom != tc.marginBottom {
				t.Errorf("safe zones = %.1f/%.1f, want %.1f/%.1f",
					tc.s.MarginTop, tc.s.MarginBottom, tc.marginTop, tc.marginBottom)
			}
			if got := round1(tc.s.BodyHeight()); got != tc.bodyHeightMM {
				t.Errorf("body height = %.1fmm, want %.1f", got, tc.bodyHeightMM)
			}
			if got := round1(tc.s.ContentWidth()); got != tc.measureMM {
				t.Errorf("measure = %.1fmm, want %.1f", got, tc.measureMM)
			}
			if tc.s.MaxKPICards != tc.maxKPICards {
				t.Errorf("kpi cards = %d, want %d", tc.s.MaxKPICards, tc.maxKPICards)
			}
			// The one property a card cap has to keep: the cards fit. Four
			// stacked cards need ~120mm, which is why square keeps three.
			if tc.s.MaxKPICards == 4 && tc.s.BodyHeight() < 120 {
				t.Errorf("body is %.1fmm and the cap is 4 cards; four need ~120mm", tc.s.BodyHeight())
			}
			// Every social surface is taller than it is wide except none of
			// them: all three stack, and the layout asks the frame.
			if !tc.s.Portrait() && tc.name != "square" {
				t.Errorf("%s does not report as portrait", tc.name)
			}
		})
	}
	// A square frame is neither portrait nor landscape, and the predicate is
	// strict: equal sides answer false, so a square lays out like the wide
	// surface. That is a choice worth pinning rather than discovering.
	if Square.Portrait() {
		t.Error("a 1:1 surface reports as portrait; the layout would stack")
	}
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
