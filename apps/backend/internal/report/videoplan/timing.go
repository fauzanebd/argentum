package videoplan

import (
	"math"
	"strings"
	"unicode"
)

// How long anything is on screen.
//
// The model writes the content and this decides the seconds (locked decision
// 5). The numbers below are a starting point tuned against the fixture set, not
// a law — **tune them here rather than working around them in a component**,
// because a scene that is on screen for the wrong length is not fixable by
// making its animation slower.
//
// The unit is frames throughout. Seconds appear only in these constants, and
// every duration is rounded to a whole frame before it leaves this file: a
// scene that is 91.5 frames long is a scene whose last frame is a rounding
// decision made somewhere else.

const (
	// FPS is the frame rate. 30 rather than 24 because the content is text and
	// masks rather than motion, and 24 shows judder on a horizontal wipe; and
	// rather than 60 because it doubles the render for movement nobody sees.
	FPS = 30

	// ReadWordsPerSecond is the pace the text is assumed to be read at.
	//
	// 2.7 is deliberately slower than silent reading (≈4/s) and faster than
	// speech (≈2.3/s). The viewer is reading text they did not write, about
	// figures they are seeing for the first time, and they cannot scroll back —
	// that last constraint is the one that matters and it is the reason this is
	// not tuned to a reading-speed study.
	ReadWordsPerSecond = 2.7

	// SceneLeadIn is the time before the copy is assumed to be legible: the
	// entrance, plus the beat it takes to look at a new frame.
	SceneLeadInSeconds = 1.2

	// MinSceneSeconds is the floor. Below this a scene reads as a flash, and a
	// run of short scenes reads as a fault.
	MinSceneSeconds = 3.5

	// MaxSceneSeconds is the ceiling. Anything that would run longer is split
	// across a continuation scene instead — which is the same answer the deck
	// gives to a block that does not fit, for the same reason.
	MaxSceneSeconds = 15.0

	// CoverSeconds, DividerSeconds and ClosingSeconds are fixed. They carry
	// almost no text, so a computed duration would flash past — and they are the
	// three moments where a fixed rhythm is what makes the thing feel authored.
	CoverSeconds   = 4.0
	DividerSeconds = 2.0
	ClosingSeconds = 3.0

	// ChartRevealSeconds is added to a chart scene before its caption is
	// assumed to be read: the mask has to finish moving before the reader is
	// looking at the shape it uncovered.
	ChartRevealSeconds = 1.5

	// TableRowSeconds is added per row. A table is not read as prose — the eye
	// scans it — but twelve rows is not the same work as three.
	TableRowSeconds = 0.45

	// KPICardSeconds is added per card, for the same reason.
	KPICardSeconds = 0.9
)

// frames converts seconds to whole frames, never fewer than one.
func frames(seconds float64) int {
	n := int(math.Round(seconds * FPS))
	return max(n, 1)
}

// clamp bounds a computed duration to the floor and the ceiling.
func clamp(seconds float64) float64 {
	return math.Min(math.Max(seconds, MinSceneSeconds), MaxSceneSeconds)
}

// readSeconds is how long the given text takes to read, before the floor and
// the ceiling are applied.
func readSeconds(parts ...string) float64 {
	n := 0
	for _, p := range parts {
		n += words(p)
	}
	if n == 0 {
		return 0
	}
	return SceneLeadInSeconds + float64(n)/ReadWordsPerSecond
}

// readingFrames is the whole computation for a scene whose length is its text:
// lead-in, reading time, floor and ceiling, rounded to frames.
func readingFrames(parts ...string) int {
	s := readSeconds(parts...)
	if s == 0 {
		s = MinSceneSeconds
	}
	return frames(clamp(s))
}

// words counts words the way a reader would, not the way strings.Fields does.
//
// A figure is one word regardless of how many digits and separators it carries
// — "Rp 3.863.405.700" is one thing the eye takes in, and splitting on spaces
// would call it two and give the scene an extra second for the currency symbol.
// So a token that is entirely punctuation or a bare currency symbol does not
// count on its own.
func words(s string) int {
	n := 0
	for _, f := range strings.Fields(s) {
		if hasLetterOrDigit(f) {
			n++
		}
	}
	return n
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// wordsIn is words() over a slice of pre-wrapped lines.
func wordsIn(lines []string) int {
	n := 0
	for _, l := range lines {
		n += words(l)
	}
	return n
}
