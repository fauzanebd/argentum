// Package docchunk cuts a parsed document into the pieces retrieval works on
// (T-P8).
//
// **Structure first, budget second.** A chunk that begins mid-sentence under
// somebody else's heading answers questions wrongly and cites confidently, so
// the cut points are the document's own headings; the token budget only decides
// where a *long* section is split, never whether a heading is crossed.
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

// Options tunes the cut. The zero value is the shipped behaviour.
type Options struct {
	// MaxTokens is the budget per chunk, counted the cheap way (see [tokens]).
	MaxTokens int
	// Overlap is how many tokens of the previous chunk are repeated at the
	// start of the next one, so a sentence that straddles a cut is retrievable
	// from either side.
	Overlap int
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

// headingLine matches a markdown heading, which is what the parser emits for a
// line it read as one, and a line that is short, unpunctuated and set apart —
// which is what a heading looks like in a PDF that has no markup at all.
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
		if page.Kind != docparse.KindText || strings.TrimSpace(page.Markdown) == "" {
			continue
		}
		for _, line := range strings.Split(page.Markdown, "\n") {
			if m := headingLine.FindStringSubmatch(line); m != nil {
				flush()
				section = newSection(headingTrail(section.trail, len(m[1]), m[2]), page.Number)
				continue
			}
			section.add(line, page.Number)
		}
	}
	flush()
	return out
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
