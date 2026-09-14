// Package evalspeech scores what a speech provider heard against what was said:
// T-W7's owed live arm, and research 08 §6's unknowns 1 and 2.
//
// Two numbers, reported apart because they fail for different reasons and only
// one of them is dangerous:
//
//   - **Word error rate**, over text whose numbers have been reduced to their
//     values. A transcript that writes "300 juta" where the speaker said "tiga
//     ratus juta" has heard every word; charging it three substitutions would
//     rank providers on spelling convention, and hide the one error that matters.
//   - **Whether the numbers came back.** Compared as values, so the form a
//     provider writes them in is not an error and a misheard multiplier is.
//     "tiga puluh juta" for "tiga ratus juta" is one syllable, a WER of one word,
//     and a question the agent then answers correctly about the wrong figure
//     (roadmap 11, decision 13).
package evalspeech

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fauzanebd/argentum/internal/numparse"
)

// Set is a reading script: what somebody says, and the numbers it holds.
type Set struct {
	// Language is the hint every provider is sent, as the product sends it.
	Language string `yaml:"language"`
	Clips    []Clip `yaml:"clips"`
}

// Clip is one line of the script. Numbers is stated rather than parsed from
// Text, so a defect in the numeral reader cannot make a reference agree with
// itself; TestTheSetStatesWhatItsTextSays holds the two together.
type Clip struct {
	ID      string    `yaml:"id"`
	Text    string    `yaml:"text"`
	Numbers []float64 `yaml:"numbers"`
}

// LoadSet reads and validates a set.
func LoadSet(path string) (*Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read speech set: %w", err)
	}
	var s Set
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse speech set: %w", err)
	}
	if len(s.Clips) == 0 {
		return nil, fmt.Errorf("speech set %s has no clips", path)
	}
	seen := map[string]bool{}
	for i, c := range s.Clips {
		switch {
		case strings.TrimSpace(c.ID) == "":
			return nil, fmt.Errorf("clip %d has no id", i+1)
		case seen[c.ID]:
			return nil, fmt.Errorf("clip id %q appears twice", c.ID)
		case strings.TrimSpace(c.Text) == "":
			return nil, fmt.Errorf("clip %q has no text", c.ID)
		}
		seen[c.ID] = true
	}
	return &s, nil
}

// Score is one clip, as one provider heard it.
type Score struct {
	ClipID     string
	Transcript string
	WER        float64
	Said       []float64
	Heard      []float64
	// NumbersRight is whether every number said came back, and no other.
	NumbersRight bool
	// DigitRuns and WordRuns count how the transcript wrote its numbers. Not
	// scored — both are right — but it is what the recorded prediction is about.
	DigitRuns int
	WordRuns  int
}

// ScoreClip scores one transcript against its line.
func ScoreClip(c Clip, transcript string) Score {
	items := tokenize(transcript)
	heard, digits, words := numbersOf(items)
	return Score{
		ClipID:       c.ID,
		Transcript:   transcript,
		WER:          WER(Words(c.Text), wordsOf(items)),
		Said:         c.Numbers,
		Heard:        heard,
		NumbersRight: SameNumbers(c.Numbers, heard),
		DigitRuns:    digits,
		WordRuns:     words,
	}
}

// Numbers reads every number a text states, in order.
func Numbers(text string) []float64 {
	v, _, _ := numbersOf(tokenize(text))
	return v
}

// Words is a text as WER compares it: lower case, punctuation gone, and every
// number reduced to one token carrying its value.
func Words(text string) []string { return wordsOf(tokenize(text)) }

// WER is the word error rate of hyp against ref: substitutions, insertions and
// deletions over the reference's length.
func WER(ref, hyp []string) float64 {
	if len(ref) == 0 {
		if len(hyp) == 0 {
			return 0
		}
		return 1
	}
	prev := make([]int, len(hyp)+1)
	cur := make([]int, len(hyp)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ref); i++ {
		cur[0] = i
		for j := 1; j <= len(hyp); j++ {
			cost := 1
			if ref[i-1] == hyp[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(hyp)]) / float64(len(ref))
}

// SameNumbers compares two lists of values as multisets. Order is not compared:
// "dua liter di bawah dua puluh ribu" and a transcript that moved a clause are
// the same numbers, and word order is WER's job.
func SameNumbers(want, got []float64) bool {
	if len(want) != len(got) {
		return false
	}
	used := make([]bool, len(got))
	for _, w := range want {
		found := false
		for i, g := range got {
			if !used[i] && closeTo(w, g) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func closeTo(a, b float64) bool {
	scale := math.Max(math.Abs(a), math.Abs(b))
	return math.Abs(a-b) <= 1e-9*math.Max(scale, 1)
}

// item is one token of a transcript: a word, or a number run.
type item struct {
	word   string
	number float64
	isNum  bool
	digits bool // the run contained a digit token
}

// The Indonesian number words. Units are what koma reads as decimal digits;
// the se- forms are a unit and its multiplier in one word.
var (
	units = map[string]float64{
		"nol": 0, "kosong": 0, "satu": 1, "dua": 2, "tiga": 3, "empat": 4,
		"lima": 5, "enam": 6, "tujuh": 7, "delapan": 8, "sembilan": 9,
	}
	seForms = map[string]float64{
		"sepuluh": 10, "sebelas": 11, "seratus": 100, "seribu": 1e3, "sejuta": 1e6, "semiliar": 1e9,
	}
)

// tokenize splits a transcript into words and number runs.
//
// A run is the longest stretch of number words and digit tokens that reads as
// one number: "seratus lima puluh ribu" is 150,000, "dua ribu dua puluh lima" is
// 2025, "Rp 2,5 juta" is 2,500,000. It ends at any other word, or where a unit
// would repeat a unit already pending ("dua tiga" is two numbers).
func tokenize(text string) []item {
	var out []item
	var p runParser
	flush := func() {
		if v, ok := p.end(); ok {
			out = append(out, item{number: v, isNum: true, digits: p.sawDigits})
		}
		p = runParser{}
	}
	for _, raw := range strings.Fields(strings.ToLower(text)) {
		tok := strings.Trim(raw, `.,?!;:"'()[]“”‘’`)
		percent := strings.HasSuffix(tok, "%")
		tok = strings.TrimSuffix(tok, "%")
		if strings.HasPrefix(tok, "rp") && len(tok) > 2 && isDigit(tok[2]) {
			tok = tok[2:]
		}
		if tok == "" {
			if percent {
				flush()
				out = append(out, item{word: "persen"})
			}
			continue
		}
		if !p.feed(tok) {
			flush()
			if !p.feed(tok) {
				// An ordinal ("ke-3"), a code, or a word: kept for WER as written.
				out = append(out, item{word: tok})
			}
		}
		if percent {
			flush()
			out = append(out, item{word: "persen"})
		}
	}
	flush()
	return out
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// runParser accumulates one number run. total holds what a magnitude has
// already closed, group the part below the next magnitude, cur the unit pending.
type runParser struct {
	total, group, cur float64
	any, sawDigits    bool
	pendingUnit       bool
	decimal           bool
	frac              string
}

// feed takes one token and reports whether it belongs to the run.
func (p *runParser) feed(tok string) bool {
	if tok[0] >= '0' && tok[0] <= '9' {
		if strings.IndexFunc(tok, func(r rune) bool { return (r < '0' || r > '9') && r != '.' && r != ',' }) >= 0 {
			return false
		}
		if p.pendingUnit || p.decimal || p.group != 0 {
			return false // a second number, not part of this one
		}
		v, ok := numparse.Parse(tok)
		if !ok {
			return false
		}
		p.cur, p.any, p.sawDigits, p.pendingUnit = v, true, true, true
		return true
	}
	if n, ok := units[tok]; ok {
		if p.decimal {
			p.frac += strconv.Itoa(int(n))
			return true
		}
		if p.pendingUnit {
			return false
		}
		p.cur, p.any, p.pendingUnit = n, true, true
		return true
	}
	if n, ok := seForms[tok]; ok {
		if p.decimal {
			return false
		}
		switch {
		case n >= 1e3:
			p.closeMagnitude(n)
		case n == 100:
			if p.pendingUnit {
				return false
			}
			p.group += 100
		default:
			if p.pendingUnit {
				return false
			}
			p.cur, p.pendingUnit = n, true
		}
		p.any = true
		return true
	}
	if !p.any {
		return false
	}
	switch tok {
	case "belas":
		if p.decimal {
			return false
		}
		p.group += p.cur + 10
		p.cur, p.pendingUnit = 0, false
		return true
	case "puluh":
		if p.decimal {
			return false
		}
		p.group += p.cur * 10
		p.cur, p.pendingUnit = 0, false
		return true
	case "ratus":
		if p.decimal {
			return false
		}
		p.group += p.cur * 100
		p.cur, p.pendingUnit = 0, false
		return true
	case "koma":
		if p.decimal {
			return false
		}
		p.decimal = true
		return true
	}
	if m, ok := numparse.Magnitude(tok); ok && m >= 1e3 {
		p.closeMagnitude(m)
		return true
	}
	return false
}

func (p *runParser) fraction() float64 {
	if p.frac == "" {
		return 0
	}
	f, _ := strconv.ParseFloat("0."+p.frac, 64)
	return f
}

func (p *runParser) closeMagnitude(m float64) {
	base := p.group + p.cur + p.fraction()
	if base == 0 {
		base = 1
	}
	p.total += base * m
	p.group, p.cur, p.frac = 0, 0, ""
	p.decimal, p.pendingUnit = false, false
}

func (p *runParser) end() (float64, bool) {
	if !p.any {
		return 0, false
	}
	return p.total + p.group + p.cur + p.fraction(), true
}

func numbersOf(items []item) (values []float64, digitRuns, wordRuns int) {
	values = []float64{}
	for _, it := range items {
		if !it.isNum {
			continue
		}
		values = append(values, it.number)
		if it.digits {
			digitRuns++
		} else {
			wordRuns++
		}
	}
	return values, digitRuns, wordRuns
}

func wordsOf(items []item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.isNum {
			out = append(out, "#"+strconv.FormatFloat(it.number, 'f', -1, 64))
			continue
		}
		out = append(out, it.word)
	}
	return out
}
