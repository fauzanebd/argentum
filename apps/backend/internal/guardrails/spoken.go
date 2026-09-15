package guardrails

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/numparse"
)

// Spoken figures: an answer read aloud may state no figure its written answer
// does not (T-W8, roadmap 11 decision 14).
//
// A spoken answer is a second statement of the same numbers, written separately,
// by a model asked to make them sayable. Rounding is the point of it — nobody
// holds "1.234.567" in their head, and "sekitar 1,2 juta" is the answer. A new
// figure is the fabrication class, arriving by a door nothing in this product
// watched: CheckGrounding asks whether a figure is one a tool returned, and the
// spoken text never saw a tool. What it saw was the written answer, so that is
// the only fair thing to hold it to, and the narrower one.
//
// **It blocks, where CheckGrounding reports.** The costs run the other way.
// CheckGrounding's false positive would replace a correct answer; a refusal here
// costs a play button that does nothing beside a written answer that still
// stands (decision 15). A false pass is a figure said out loud that the page
// does not hold, to someone listening precisely because they are not reading.

// SpokenFigureError is a figure in a spoken text that its written answer does
// not state — the T-P5 shape, naming both figures.
type SpokenFigureError struct {
	// Spoken is the figure as the spoken text wrote it, magnitude word included.
	Spoken string
	// Written is the written answer's nearest figure, as written. Empty when the
	// written answer states none.
	Written string
	// Spelled says the spoken figure was written in words, which this check
	// cannot read.
	Spelled bool
}

func (e *SpokenFigureError) Error() string {
	switch {
	case e.Spelled:
		return fmt.Sprintf("spoken %q spells a figure in words; only a figure written in digits can be checked against the written answer", e.Spoken)
	case e.Written == "":
		return fmt.Sprintf("spoken %s, written none: the written answer states no figure", e.Spoken)
	default:
		return fmt.Sprintf("spoken %s, nearest written %s: not the written figure at the precision it was spoken", e.Spoken, e.Written)
	}
}

// preciseFigure is one number a text states, and how precisely.
type preciseFigure struct {
	// raw is the figure as written, for the refusal sentence.
	raw   string
	value float64
	// unit is the size of the last digit the writer committed to, times its
	// magnitude: 0.1 million for "1.2 million", one for "1,234,567", a hundred
	// for "300".
	unit float64
}

// preciseFigurePattern is a run of digits, the magnitude word that scales it,
// and a percent sign or word — the last only so a spelled-figure scan after it
// does not see "persen" on its own.
var preciseFigurePattern = regexp.MustCompile(
	`(?i)(\d[\d.,]*)(?:\s*(` + strings.Join(numparse.MagnitudeWords(), "|") + `)\b)?(?:\s*(?:%|persen\b|percent\b))?`)

// spelledNumberWords are the words a figure spelled in words cannot be written
// without, in both languages this product answers in. A magnitude word
// (numparse.Magnitude) is one too, when no digits stand in front of it.
//
// **The units — "satu", "dua", "one", "three" — are deliberately absent.** "Salah
// satu gudang" is "one of the warehouses", and "no one" is not a count; refusing
// every one of them would refuse half the spoken answers in either language. The
// cost is written down rather than hidden: a count under ten spelled as a word is
// not checked, and the reduction prompt asks for digits for that reason.
var spelledNumberWords = map[string]bool{
	"puluh": true, "belas": true, "ratus": true, "koma": true, "persen": true,
	"sepuluh": true, "sebelas": true, "seratus": true, "seribu": true, "sejuta": true,
	"semiliar": true, "setriliun": true,
	"ten": true, "eleven": true, "twelve": true, "thirteen": true, "fourteen": true,
	"fifteen": true, "sixteen": true, "seventeen": true, "eighteen": true, "nineteen": true,
	"twenty": true, "thirty": true, "forty": true, "fifty": true, "sixty": true,
	"seventy": true, "eighty": true, "ninety": true, "hundred": true, "percent": true,
}

var letterRun = regexp.MustCompile(`\p{L}+`)

// Written-answer spans whose digits are not figures the answer states.
var (
	spokenCheckCode = regexp.MustCompile("```[\\s\\S]*?```|`[^`]*`")
	spokenCheckLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	spokenCheckURL  = regexp.MustCompile(`https?://\S+`)
)

// CheckSpokenFigures holds every figure in spoken to a figure in written.
//
// **A spoken figure passes when some written figure rounds to it at the
// precision it was spoken.** "1.2 million" is spoken to the nearest hundred
// thousand, and 1,234,567 rounds to it; "2 million" is spoken to the nearest
// million, and 1,234,567 does not. Precision is read off the digits: decimal
// places, or for a whole number its trailing zeros — "300" is to the nearest
// hundred, which is how a person rounding 312 writes it — except a bare year,
// which is exact.
//
// **A spoken figure finer than its written one needs no rule of its own.**
// "3,8634 miliar" beside "Rp 3,86 Miliar" is refused by the rounding above: at
// the spoken precision the two are 3.4 million apart, and a finer figure can only
// agree with a coarser one by padding it with zeros ("1,20 juta" for "1,2 juta"),
// which states nothing new. A clause refusing finer precision outright was built,
// survived its mutation, and was taken out, because the only thing it refused
// that the rounding does not was that padding — including a correct "1,0 juta".
//
// Table cells are figures the answer states; code and URLs are not. Sign is not
// compared — "turun 5%" and "-5%" say the same thing in different places — and
// a percentage compares as its number. What this cannot check is a figure it
// cannot read, so a figure spelled in words is refused outright rather than
// passed unexamined.
func CheckSpokenFigures(written, spoken string) error {
	if e := spelledFigure(spoken); e != nil {
		return e
	}
	said := preciseFigures(spoken)
	if len(said) == 0 {
		return nil
	}
	stated := preciseFigures(writtenProse(written))
	for _, s := range said {
		held := false
		for _, w := range stated {
			if roundsTo(w, s) {
				held = true
				break
			}
		}
		if held {
			continue
		}
		e := &SpokenFigureError{Spoken: s.raw}
		if n, ok := nearestFigure(stated, s); ok {
			e.Written = n.raw
		}
		return e
	}
	return nil
}

// roundsTo reports whether the written figure, rounded to the spoken one's
// precision, is the spoken one. The slack is a float parse's width, not
// tolerance.
func roundsTo(written, spoken preciseFigure) bool {
	const eps = 1e-9
	slack := eps * math.Max(math.Abs(spoken.value), 1)
	return math.Abs(written.value-spoken.value) <= spoken.unit/2+slack
}

func nearestFigure(stated []preciseFigure, s preciseFigure) (preciseFigure, bool) {
	var best preciseFigure
	found, gap := false, math.Inf(1)
	for _, w := range stated {
		if d := math.Abs(w.value - s.value); d < gap {
			best, gap, found = w, d, true
		}
	}
	return best, found
}

// preciseFigures reads every figure a text states, with its precision.
func preciseFigures(text string) []preciseFigure {
	var out []preciseFigure
	for _, m := range preciseFigurePattern.FindAllStringSubmatchIndex(text, -1) {
		tok := strings.Trim(text[m[2]:m[3]], ".,")
		v, places, ok := numparse.ParsePlaces(tok)
		if !ok {
			continue
		}
		unit := math.Pow10(-places)
		if places == 0 && !isBareYear(tok, v) {
			unit = math.Pow10(trailingZeros(tok))
		}
		if m[4] >= 0 {
			if mult, ok := numparse.Magnitude(text[m[4]:m[5]]); ok {
				v *= mult
				unit *= mult
			}
		}
		raw := strings.TrimRight(strings.TrimSpace(text[m[0]:m[1]]), ".,")
		out = append(out, preciseFigure{raw: raw, value: v, unit: unit})
	}
	return out
}

// trailingZeros counts the zeros a whole number ends in, separators ignored. A
// number that is all zeros has none worth counting: "0" is exact.
func trailingZeros(tok string) int {
	n := 0
	for i := len(tok) - 1; i >= 0; i-- {
		switch tok[i] {
		case '.', ',':
			continue
		case '0':
			n++
		default:
			return n
		}
	}
	return 0
}

// spelledFigure finds a figure written in words: a word no figure can be spelled
// without, or a magnitude word with no digits in front of it. The digit figures
// are blanked first, so "1,2 juta" leaves nothing and "dua juta" leaves "juta".
func spelledFigure(spoken string) *SpokenFigureError {
	words := letterRun.FindAllString(preciseFigurePattern.ReplaceAllString(spoken, " "), -1)
	for i, w := range words {
		lw := strings.ToLower(w)
		_, magnitude := numparse.Magnitude(lw)
		if !magnitude && !spelledNumberWords[lw] {
			continue
		}
		phrase := w
		if i > 0 {
			phrase = words[i-1] + " " + w
		}
		return &SpokenFigureError{Spoken: phrase, Spelled: true}
	}
	return nil
}

// writtenProse is the written answer without the spans whose digits are not
// figures it states: code, and URLs. A link's text stays; its target goes.
func writtenProse(s string) string {
	s = spokenCheckCode.ReplaceAllString(s, " ")
	s = spokenCheckLink.ReplaceAllString(s, "$1")
	return spokenCheckURL.ReplaceAllString(s, " ")
}
