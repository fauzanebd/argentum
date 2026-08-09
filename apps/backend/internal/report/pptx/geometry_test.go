package pptx

import (
	"math"
	"testing"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/measure"
)

// The deck derives its slide from OOXML's EMU and the video derives its frame
// from canvas's millimetres. These tests are the only thing making those the
// same surface.
//
// They exist because the first version of canvas wrote 338.667 as a literal.
// It is 0.00033mm wider than 12192000 ÷ 36000, every test still passed, and all
// five deck fixtures changed — between 29 and 375 bytes each — because that
// difference reaches every measured column width and every EMU rounding in the
// package. A comment saying "these match" would not have caught it.

func TestSlideAndCanvasAgree(t *testing.T) {
	if got, want := float64(slideWidthEMU)/emuPerMM, canvas.WidthMM; math.Abs(got-want) > 1e-12 {
		t.Errorf("slide is %.10fmm wide, canvas is %.10fmm", got, want)
	}
	if got, want := slideHeightMM, canvas.HeightMM; math.Abs(got-want) > 1e-12 {
		t.Errorf("slide is %.10fmm tall, canvas is %.10fmm", got, want)
	}
}

// TestTwoPixelsPerPoint pins the relationship the video renderer's whole
// layout rests on: 1920px across the slide's width is exactly 2px per point,
// so a 29pt slide title is a 58px frame title and a line that fits on one fits
// on the other.
func TestTwoPixelsPerPoint(t *testing.T) {
	if got := canvas.PxPerPt; math.Abs(got-2.0) > 1e-9 {
		t.Errorf("px per point is %.12f, want exactly 2", got)
	}
	if got := 1080.0 / canvas.PxPerMM; math.Abs(got-canvas.HeightMM) > 1e-9 {
		t.Errorf("1080px is %.6fmm, but the surface is %.6fmm tall — the frame is not 16:9 against it", got, canvas.HeightMM)
	}
	if got := canvas.PtPx(canvas.Type.H1); got != 58 {
		t.Errorf("H1 is %dpx, want 58", got)
	}
}

// TestAlignmentValuesAgree: the table solver moved to canvas and writes the
// alignment strings the deck's OOXML consumes. A mismatch would silently
// left-align every currency column.
func TestAlignmentValuesAgree(t *testing.T) {
	for _, pair := range [][2]string{
		{alignLeft, canvas.AlignLeft},
		{alignCenter, canvas.AlignCenter},
		{alignRight, canvas.AlignRight},
	} {
		if pair[0] != pair[1] {
			t.Errorf("pptx writes %q where canvas writes %q", pair[0], pair[1])
		}
	}
}

// TestLineHeightIsMaroto guards the one constant both renderers inherit from a
// dependency rather than from a token.
func TestLineHeightIsMaroto(t *testing.T) {
	if got := measure.LineHeightMM(10); math.Abs(got-3.5277777) > 1e-6 {
		t.Errorf("10pt is %.6fmm, want 3.527778", got)
	}
}
