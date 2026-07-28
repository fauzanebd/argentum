package theme

import (
	"fmt"
	"math"
	"strings"
)

// White and Black are the two surfaces a document colour is judged against:
// paper, and the deck's dark cover. They are not tokens — a token is a value
// the design system chose, and these two are physics.
var (
	White = Color{R: 0xFF, G: 0xFF, B: 0xFF}
	Black = Color{R: 0x00, G: 0x00, B: 0x00}
)

// ParseHexColor reads `#RRGGBB` or `RRGGBB`, case-insensitively.
//
// Three-digit shorthand is deliberately rejected. It is a CSS convenience, and
// accepting it here would mean the value stored for a tenant is not the value
// they typed — which matters when the next person compares the branding record
// against a brand guideline.
func ParseHexColor(s string) (Color, error) {
	raw := strings.TrimSpace(s)
	raw = strings.TrimPrefix(raw, "#")
	if len(raw) != 6 {
		return Color{}, fmt.Errorf("colour must be #RRGGBB, got %q", s)
	}
	var v [3]uint8
	for i := range 3 {
		hi, err := hexDigit(raw[i*2])
		if err != nil {
			return Color{}, fmt.Errorf("colour must be #RRGGBB, got %q", s)
		}
		lo, err := hexDigit(raw[i*2+1])
		if err != nil {
			return Color{}, fmt.Errorf("colour must be #RRGGBB, got %q", s)
		}
		v[i] = hi<<4 | lo
	}
	return Color{R: v[0], G: v[1], B: v[2]}, nil
}

func hexDigit(b byte) (uint8, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	}
	return 0, fmt.Errorf("not a hex digit: %q", string(b))
}

// RelativeLuminance is WCAG 2.1's L, the sRGB channels linearised and weighted
// by the eye's sensitivity.
//
// This is a different question from the one packages/design-tokens/scripts/palette.mjs
// asks of the chart palette: that checks *separability* — can two series be
// told apart in greyscale and under simulated CVD — while this checks
// *readability* against a single background. A tenant's accent is judged here
// because it is drawn as text and rules on paper, not as one of eight
// competing series.
func (c Color) RelativeLuminance() float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// ContrastRatio is WCAG 2.1's (L1+0.05)/(L2+0.05), between 1 and 21.
func ContrastRatio(a, b Color) float64 {
	la, lb := a.RelativeLuminance(), b.RelativeLuminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// MinBrandContrast is the floor a tenant's accent must clear against paper.
//
// 3:1 rather than 4.5:1 because of what the accent is used for: section rules,
// KPI figures at 24pt, and headings — all of them large text or non-text
// graphics, which is exactly the boundary WCAG puts at 3:1. Requiring 4.5:1
// would reject brand colours that are perfectly readable at the sizes this
// renderer draws them, and a rule a customer's own brand guideline fails is a
// rule they will ask to have removed.
const MinBrandContrast = 3.0

// Readable returns c if it clears min against bg, and otherwise the nearest
// mix of c towards away that does.
//
// The deck's cover is near-black, so an accent picked to sit on white can
// vanish on it. Rejecting such a colour at configuration time would be wrong —
// it is *correct* for the PDF, which is the artifact most tenants care about —
// so the deck lightens it for that one surface instead, keeping the hue.
//
// Returns the input unchanged when no mix reaches min (a colour equal to the
// background it is drawn on cannot be rescued by mixing towards it).
func Readable(c, bg, away Color, min float64) Color {
	if ContrastRatio(c, bg) >= min {
		return c
	}
	// 32 steps: mixing is monotone in t here (away is white or black, so each
	// step moves luminance one way), and 1/32 is finer than the eye reads on a
	// projector. A binary search would be exact and unreadable for one lerp.
	for i := 1; i <= 32; i++ {
		cand := c.Mix(away, float64(i)/32)
		if ContrastRatio(cand, bg) >= min {
			return cand
		}
	}
	return c
}
