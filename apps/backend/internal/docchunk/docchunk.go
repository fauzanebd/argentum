// Package docchunk cuts a parsed document into the pieces retrieval works on
// (T-P8).
//
// **Structure first, budget second — where there is structure to find.** A
// chunk that begins mid-sentence under somebody else's heading answers
// questions wrongly and cites confidently, so a heading is never crossed and
// the token budget only decides where a *long* section is split.
//
// **What a heading is here, and what it was not.** This package was written
// against markdown headings and shipped with no test file, and the live gate of
// 2026-08-19 found the consequence: `apps/docparse` renders a page as its text
// plus its tables as GFM pipe tables and emits no `#` on any path, so the
// markdown branch below had never matched a line on any deployment. Every
// heading_path was empty and every cut was the token budget's. The markdown
// branch stays — a hosted parser swapped in behind `docparse.Parser` does emit
// headings — and [Options.DetectHeadings] is the branch for a parse with no
// markup at all, off until the eval set says what it does to retrieval.
//
// **A table is never split.** A half table is a quotation machine for the wrong
// number: the model sees three of five rows, the totals are in the two it
// cannot see, and every instrument in this product is looking somewhere else.
// Where a table does not fit the budget it becomes its own oversized chunk,
// which is the lesser failure — and the tables have a better path anyway
// (T-P6), so prose retrieval is not where a figure should be coming from.
//
// Everything here is a pure function over the parse artifact. No model, no
// database, no network: the same rule `internal/doctable` follows, for the same
// reason — this is the code whose mistakes are invisible in the output.
package docchunk

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/fauzanebd/argentum/internal/docparse"
)

// Chunk is one retrievable piece of a document.
type Chunk struct {
	Ordinal  int `json:"ordinal"`
	PageFrom int `json:"page_from"`
	PageTo   int `json:"page_to"`
	// HeadingPath is the trail this chunk sits under: "Bab 3 › Ketentuan
	// Pembayaran". It is shown in a citation and prepended to what is embedded,
	// because a paragraph read without its heading is a paragraph about
	// nothing.
	HeadingPath string `json:"heading_path"`
	Content     string `json:"content"`
	// HasTable says this chunk holds a table's markdown. Carried so a caller
	// can prefer the warehouse path for figures — a number in a table has a
	// better door than a quotation.
	HasTable bool `json:"has_table"`
}

// Options tunes the cut. The zero value is a 500-token budget with **no
// overlap** — the shipped 60 comes from `DOC_CHUNK_OVERLAP`, and the 60 in
// [Options.withDefaults] is the fallback for an unusable value, not the default
// for an unset one.
type Options struct {
	// MaxTokens is the budget per chunk, counted the cheap way (see [tokens]).
	MaxTokens int
	// Overlap is how many tokens of the previous chunk are repeated at the
	// start of the next one, so a sentence that straddles a cut is retrievable
	// from either side.
	Overlap int
	// DetectHeadings cuts on lines that *look* like headings in a parse that
	// carries no markup: short, set apart by a blank line, not ending a
	// sentence, not a figure sitting on its own.
	//
	// **Off by default, and the reason is a number nobody has.** Turning it on
	// moves every chunk boundary in every document with no markdown in it,
	// which is all of them on the current sidecar — that is a retrieval change,
	// and this repository's rule is that an unmeasured change is an unshipped
	// one. `make eval-docs` is what decides it: T-P13's answer-correctness
	// score with DOC_CHUNK_DETECT_HEADINGS off, then on, on the same corpus.
	DetectHeadings bool
}

func (o Options) withDefaults() Options {
	if o.MaxTokens <= 0 {
		o.MaxTokens = 500
	}
	if o.Overlap < 0 || o.Overlap >= o.MaxTokens {
		o.Overlap = 60
	}
	return o
}

// headingLine matches a markdown heading. **Only a markdown heading** — the
// sentence this comment used to carry, that it also matched a line which is
// short, unpunctuated and set apart, described [looksLikeHeading], which did
// not exist. `apps/docparse` emits no `#`, so this pattern matches nothing this
// deployment parses; it is here for a parser that does.
var headingLine = regexp.MustCompile(`^(#{1,6})\s+(.*\S)\s*$`)

// tableLine matches a markdown table row. Used to keep a table together, not to
// parse it: parsing tables is `internal/doctable`'s job and it works from the
// cell grid rather than from rendered pipes.
var tableLine = regexp.MustCompile(`^\s*\|.*\|\s*$`)

// Build cuts a document into chunks.
//
// Pages arrive in order and pages nobody read contribute nothing — a scan in
// the middle of a born-digital report is a page with no text, not a reason to
// end the section around it.
func Build(pages []docparse.Page, opts Options) []Chunk {
	opts = opts.withDefaults()

	var (
		out     []Chunk
		section = newSection(nil, 0)
	)
	flush := func() {
		if section.empty() {
			return
		}
		out = append(out, section.split(opts, len(out))...)
	}

	for _, page := range pages {
		if !readable(page.Kind) || strings.TrimSpace(page.Markdown) == "" {
			continue
		}
		// The top of a page is set apart the way a blank line is: a title on
		// page one and a heading after a page break look the same to a reader.
		prevBlank := true
		for _, line := range strings.Split(page.Markdown, "\n") {
			if m := headingLine.FindStringSubmatch(line); m != nil {
				flush()
				section = newSection(headingTrail(section.trail, len(m[1]), m[2]), page.Number)
				prevBlank = false
				continue
			}
			if opts.DetectHeadings && prevBlank && looksLikeHeading(line) {
				flush()
				// Level 1, always: a parse with no markup carries no depth, and
				// guessing one from indentation would build a trail that says
				// something the document does not.
				section = newSection(headingTrail(nil, 1, line), page.Number)
				prevBlank = false
				continue
			}
			section.add(line, page.Number)
			prevBlank = strings.TrimSpace(line) == ""
		}
	}
	flush()
	return out
}

// readable says whether a page kind carries text worth chunking.
//
// `ocr` belongs here and was missing until 2026-08-19: T-P3 sets the kind to
// `ocr` and the markdown to what the model read, so excluding it meant a
// scanned document was rendered, sent to a model, paid for per page — and then
// held no retrievable prose at all, `search_documents` answering "nothing
// matched" about a document whose every page had been read. `needs_ocr` and
// `failed` stay out: those pages hold no text to cut.
func readable(kind string) bool {
	return kind == docparse.KindText || kind == docparse.KindOCR
}

// section accumulates the lines under one heading.
type section struct {
	trail    []string
	lines    []string
	pageFrom int
	pageTo   int
	hasTable bool
}

func newSection(trail []string, page int) *section {
	return &section{trail: trail, pageFrom: page, pageTo: page}
}

func (s *section) empty() bool {
	for _, l := range s.lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

func (s *section) add(line string, page int) {
	if s.pageFrom == 0 {
		s.pageFrom = page
	}
	s.pageTo = page
	if tableLine.MatchString(line) {
		s.hasTable = true
	}
	s.lines = append(s.lines, line)
}

// split turns one section into as many chunks as the budget requires.
//
// The unit of splitting is a *block* — a run of lines with no blank line in it —
// and a table is one block however long it is. So a section under budget is one
// chunk, a long section is cut between paragraphs, and a table that exceeds the
// budget on its own becomes an oversized chunk rather than two useless ones.
func (s *section) split(opts Options, startOrdinal int) []Chunk {
	heading := strings.Join(s.trail, " › ")
	blocks := blocksOf(s.lines)

	var (
		out     []Chunk
		current []string
		count   int
	)
	emit := func() {
		if len(current) == 0 {
			return
		}
		body := strings.TrimSpace(strings.Join(current, "\n\n"))
		if body == "" {
			current, count = nil, 0
			return
		}
		out = append(out, Chunk{
			Ordinal:     startOrdinal + len(out),
			PageFrom:    s.pageFrom,
			PageTo:      s.pageTo,
			HeadingPath: heading,
			Content:     body,
			HasTable:    s.hasTable && strings.Contains(body, "|"),
		})
		if opts.Overlap > 0 {
			tail := lastTokens(body, opts.Overlap)
			current, count = []string{tail}, tokens(tail)
			return
		}
		current, count = nil, 0
	}

	for _, block := range blocks {
		n := tokens(block)
		if count+n > opts.MaxTokens && count > 0 {
			emit()
		}
		current = append(current, block)
		count += n
		if count > opts.MaxTokens && len(current) == 1 {
			// One block over budget on its own — an oversized table, or a wall
			// of text with no paragraph breaks. Emitted whole: cutting it would
			// split the one shape this package promises not to split.
			emit()
		}
	}
	emit()
	return out
}

// blocksOf groups lines into paragraphs, keeping a table's rows in one block.
func blocksOf(lines []string) []string {
	var (
		out   []string
		buf   []string
		inTbl bool
	)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		if block := strings.TrimRight(strings.Join(buf, "\n"), "\n"); strings.TrimSpace(block) != "" {
			out = append(out, block)
		}
		buf = nil
	}
	for _, line := range lines {
		isTable := tableLine.MatchString(line)
		switch {
		case isTable && !inTbl:
			// A table starts. Whatever preceded it is its own block, so the
			// table can be kept whole without dragging a paragraph along.
			flush()
			inTbl = true
		case !isTable && inTbl:
			flush()
			inTbl = false
		}
		if strings.TrimSpace(line) == "" && !inTbl {
			flush()
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

// Bounds on what a heading can look like with no markup to prove it. Both are
// generous: the cost of missing a heading is the behaviour this package had
// before detection existed, and the cost of inventing one is a chunk boundary
// in the wrong place — so the guards below all fail towards "not a heading".
const (
	headingMaxWords = 8
	headingMaxRunes = 80
)

// figureLine matches a number written with thousand separators, or a line
// opening with a currency word. A total sitting alone under a blank line is set
// apart exactly like a heading and is the commonest thing in this corpus that
// is not one — `Rp 3.377.718.500` on its own line, which T-P4 also had to learn
// to tell apart from a phone number.
var figureLine = regexp.MustCompile(`\d[.,]\d{3}|^(?:Rp|IDR|USD|EUR|\$|€|£)\b`)

// enumPrefix is the numbering a heading may carry: "3. ", "3.1 ", "4) ". It is
// stripped before the capitalisation test and nowhere else — the number stays
// in the heading path, because "3. Ketentuan Pembayaran" is how the document
// refers to itself.
var enumPrefix = regexp.MustCompile(`^\d+(?:\.\d+)*[.)]?\s+`)

// looksLikeHeading reports whether a line is a heading in a document that has
// no markup left to say so. The caller has already established that it is set
// apart — preceded by a blank line or by the top of a page — which is the half
// of the judgement this function cannot make.
//
// Every rule here is a shape, not a vocabulary: nothing depends on the
// document's language, because the first corpus this ran against is Indonesian
// and the second will not be.
func looksLikeHeading(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || tableLine.MatchString(t) || figureLine.MatchString(t) {
		return false
	}
	runes := []rune(t)
	if len(runes) > headingMaxRunes || len(strings.Fields(t)) > headingMaxWords {
		return false
	}
	// A heading does not end a sentence. A colon does not disqualify one:
	// "Ketentuan Pembayaran:" is a heading with a colon after it.
	switch runes[len(runes)-1] {
	case '.', ',', ';', '!', '?':
		return false
	}
	body := []rune(enumPrefix.ReplaceAllString(t, ""))
	if len(body) == 0 {
		// A bare enumerator, or a page number. Neither is a heading, and both
		// are set apart by blank lines on almost every page.
		return false
	}
	// Upper case rather than "has a letter", because a lower-case opening is
	// what a continuation line looks like — the false positive that would cut a
	// paragraph in half at a page break.
	return unicode.IsUpper(body[0])
}

// headingTrail returns the heading path for a new heading at this level:
// everything shallower stays, everything deeper goes.
func headingTrail(trail []string, level int, text string) []string {
	out := make([]string, 0, level)
	for i := 0; i < level-1 && i < len(trail); i++ {
		out = append(out, trail[i])
	}
	return append(out, strings.TrimSpace(text))
}

// tokens counts tokens the cheap way: whitespace-separated words, times a
// constant for the sub-word pieces a real tokenizer would find.
//
// **Deliberately approximate.** The budget decides how much text goes in a
// chunk, and being 20% out changes retrieval quality by nothing measurable —
// where pulling in a tokenizer would add a dependency, a vocabulary file and a
// per-model correctness question to a package whose whole value is that it is
// pure and obvious. The constant is on the safe side: it over-counts, so chunks
// come out under budget rather than over.
func tokens(s string) int {
	words := len(strings.Fields(s))
	return words + words/3
}

// lastTokens returns roughly the last n tokens of a chunk, for the overlap.
func lastTokens(s string, n int) string {
	fields := strings.Fields(s)
	// The inverse of tokens()' fudge, so an overlap of 60 tokens is about 60
	// tokens rather than 80.
	want := n * 3 / 4
	if want <= 0 || len(fields) <= want {
		return s
	}
	return strings.Join(fields[len(fields)-want:], " ")
}
