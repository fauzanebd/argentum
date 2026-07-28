package theme

import (
	"math"
	"testing"
)

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in   string
		want Color
		ok   bool
	}{
		{"#F25C5C", Color{0xF2, 0x5C, 0x5C}, true},
		{"f25c5c", Color{0xF2, 0x5C, 0x5C}, true},
		{"  #0A0A0A  ", Color{0x0A, 0x0A, 0x0A}, true},
		{"#FFFFFF", White, true},
		// Shorthand is rejected on purpose: what a tenant typed and what we
		// stored have to be the same string.
		{"#FFF", Color{}, false},
		{"#GGGGGG", Color{}, false},
		{"", Color{}, false},
		{"#F25C5C5C", Color{}, false},
	}
	for _, tc := range cases {
		got, err := ParseHexColor(tc.in)
		if tc.ok && err != nil {
			t.Errorf("ParseHexColor(%q) = error %v, want %s", tc.in, err, tc.want.Hex())
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("ParseHexColor(%q) = %s, want an error", tc.in, got.Hex())
			}
			continue
		}
		if got != tc.want {
			t.Errorf("ParseHexColor(%q) = %s, want %s", tc.in, got.Hex(), tc.want.Hex())
		}
	}
}

// The two anchors of the WCAG scale, and one value checked against the figure
// the published formula gives, so a refactor of the linearisation cannot drift
// quietly.
func TestContrastRatio(t *testing.T) {
	if got := ContrastRatio(White, White); math.Abs(got-1) > 1e-9 {
		t.Errorf("white on white = %.4f, want 1", got)
	}
	if got := ContrastRatio(Black, White); math.Abs(got-21) > 1e-9 {
		t.Errorf("black on white = %.4f, want 21", got)
	}
	if got, want := ContrastRatio(Color{0x77, 0x77, 0x77}, White), 4.4780; math.Abs(got-want) > 5e-4 {
		t.Errorf("#777777 on white = %.4f, want %.4f", got, want)
	}
	// Order must not matter.
	if a, b := ContrastRatio(ColorPrimary, White), ContrastRatio(White, ColorPrimary); a != b {
		t.Errorf("ContrastRatio is not symmetric: %.4f vs %.4f", a, b)
	}
}

// The brand red is the default a tenant's colour replaces, so it has to clear
// the floor the tenant's colour is held to. If this ever fails, either the
// token moved or the floor did, and one of them is wrong.
func TestBrandRedClearsTheFloor(t *testing.T) {
	if got := ContrastRatio(ColorPrimary, White); got < MinBrandContrast {
		t.Errorf("ColorPrimary on white = %.2f:1, below the %.1f:1 floor tenants are held to",
			got, MinBrandContrast)
	}
}

func TestReadableLightensOnlyWhenItMust(t *testing.T) {
	// Navy on the deck's near-black cover: unreadable, must be lifted.
	navy := Color{0x1C, 0x3A, 0x62}
	lifted := Readable(navy, ColorForeground, White, MinBrandContrast)
	if lifted == navy {
		t.Fatalf("navy on near-black was left at %.2f:1", ContrastRatio(navy, ColorForeground))
	}
	if got := ContrastRatio(lifted, ColorForeground); got < MinBrandContrast {
		t.Errorf("lifted navy = %s at %.2f:1, still below the floor", lifted.Hex(), got)
	}

	// The brand red already clears it, so it must come back untouched — a
	// document whose accent shifts between formats is worse than one that is
	// slightly dark on one of them.
	if got := Readable(ColorPrimary, ColorForeground, White, MinBrandContrast); got != ColorPrimary {
		t.Errorf("Readable moved a colour that already passed: %s -> %s",
			ColorPrimary.Hex(), got.Hex())
	}
}

// A colour cannot be mixed away from a background it is identical to, and the
// caller gets the input back rather than a black slide.
func TestReadableGivesUp(t *testing.T) {
	if got := Readable(White, White, White, MinBrandContrast); got != White {
		t.Errorf("Readable(white, white, white) = %s, want white unchanged", got.Hex())
	}
}
