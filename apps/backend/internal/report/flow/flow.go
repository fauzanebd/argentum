// Package flow cuts a document's section list into the beats a projected
// renderer draws.
//
// The spec is a flow — a document is a single column that runs until it stops —
// and neither a deck nor a video is. So this package is where the flow is cut,
// and every decision in it is about where the cuts go.
//
// It was the deck renderer's builder until the video renderer needed the same
// cuts (T-V1). Extracting it is not tidiness: the two renderers project the same
// spec, and a table that continues onto a second slide in the deck has to
// continue onto a second scene in the video, or the same report has quietly
// become two reports. T-R4 made that argument about column widths and answered
// it with internal/report/layout; this is the same answer one level up.
//
// Three rules decide the shape, unchanged from the deck they came from:
//
//  1. One idea per beat. A KPI row, a chart, a table and a block of prose are
//     four beats, never one beat with four things in it, because the audience is
//     three metres away or watching it go past.
//  2. The prose goes in the notes. A paragraph the model wrote is three or four
//     sentences of explanation; on a slide it is a wall of text nobody reads.
//     What survives on the surface is its lead, and the whole paragraph goes to
//     the notes — which the deck prints as speaker notes and the video's player
//     shows beside the frame.
//  3. Nothing is clipped silently. This package decides *what* continues; the
//     renderer decides how far its own surface stretches before it has to.
//
// What is deliberately **not** here: anything measured. How many rows fit, how
// many bullets fit, how tall a callout is — those are surface questions with
// different answers on a slide and in a frame, and a shared answer would be
// wrong for both. This package answers "what is this beat and what is it
// called"; the renderer answers "how much of it fits".
package flow

import (
	"strings"

	"github.com/fauzanebd/argentum/internal/report/spec"
)

// Sink receives the beats, in order. Every method is a beat except AttachSource,
// which annotates the one before it.
//
// A renderer implements this and keeps its own packing, measuring and drawing.
// The methods that can fail are the two that build something before they place
// it — a chart is rasterised and a table is measured and solved — and their
// errors stop the walk, because a document missing its chart is not a document
// worth finishing.
type Sink interface {
	// Cover is the document's cover section. It arrives first, before any
	// section in the list, and only when the document has one — an invoice does
	// not want a cover page and the model should not be forced to ask for one.
	Cover(sec spec.Section)

	// Divider opens a level-1 section.
	Divider(title string)

	// Prose is the buffered run of paragraphs, callouts and orphaned footnotes
	// between two other beats. It arrives as a batch rather than one at a time
	// because a renderer packs them together, and because the notes for a run
	// belong to the beat the run starts.
	Prose(title string, sections []spec.Section)

	// Hero is one statement filling a beat (T-G11): a headline, an optional
	// kicker above it and one supporting line below. It takes no title,
	// because a hero *is* the title — a heading above it would be a second
	// voice on a frame whose whole design is one.
	Hero(sec spec.Section)

	// Promo is a retail promotion card (T-G12): a product, a photograph and
	// two prices. Like Hero it takes no title — the product's name is the
	// card's own — and for the same reason it is a beat rather than prose.
	Promo(sec spec.Section)

	// Facts is a key_value block: an invoice header, a set of parameters.
	Facts(title string, sec spec.Section)

	// KPI is a kpi_row.
	KPI(title string, sec spec.Section)

	// Chart is a chart section. The renderer rasterises it at its own scale.
	Chart(title string, sec spec.Section) error

	// Table is a table section, or the document's bare content.table. The
	// renderer decides how many rows fit and how many beats that takes.
	Table(title string, t *spec.Table) error

	// Closing is the last beat. It takes no arguments: everything on it —
	// the title, the brand, the timestamp — the renderer already holds.
	Closing()

	// AttachSource offers a footnote to the beat that was just emitted, which
	// is what a footnote annotates: a table's source, a chart's methodology.
	// It returns false when there is nothing to attach to, and the walker then
	// treats the footnote as prose.
	AttachSource(text string) bool
}

// Walk cuts the sections into beats and hands them to the sink.
//
// cover may be nil. docTitle is the fallback title for a beat that has no
// heading above it — see title.
func Walk(sections []spec.Section, cover *spec.Section, docTitle string, sink Sink) error {
	w := &walker{sink: sink, docTitle: strings.TrimSpace(docTitle)}

	if cover != nil {
		sink.Cover(*cover)
		w.emitted = true
	}
	for _, sec := range sections {
		if err := w.beat(sec); err != nil {
			return err
		}
	}
	w.flushProse()
	sink.Closing()
	return nil
}

type walker struct {
	sink     Sink
	docTitle string

	// section is the current level-1 heading, used to title the beats under it.
	// A KPI row three sections into a report is not titled "KPI row".
	section string

	// pending is the title a level-2 heading set for the next beat, and the
	// prose waiting to become one.
	pending      string
	pendingProse []spec.Section

	// emitted says whether any beat has been produced yet, which is the only
	// thing AttachSource needs to know that the sink cannot be asked.
	emitted bool
}

func (w *walker) beat(sec spec.Section) error {
	switch sec.Type {
	case spec.SectionCover:
		// Emitted first, from the document, or ignored in v1.
		return nil

	case spec.SectionHeading:
		w.flushProse()
		if sec.Level == 2 {
			// A level-2 heading titles the next beat rather than taking one of
			// its own. A divider for every sub-heading turns a ten-slide deck
			// into twenty-five, half of them one word long.
			w.pending = strings.TrimSpace(sec.Text)
			return nil
		}
		w.section = strings.TrimSpace(sec.Text)
		w.pending = ""
		w.sink.Divider(w.section)
		w.emitted = true
		return nil

	case spec.SectionParagraph, spec.SectionCallout:
		w.pendingProse = append(w.pendingProse, sec)
		return nil

	case spec.SectionFootnote:
		// A footnote annotates whatever came before it, so it goes into that
		// beat's notes rather than onto a beat of its own. With nothing before
		// it, it is prose.
		text := strings.TrimSpace(sec.Text)
		if text == "" {
			return nil
		}
		if len(w.pendingProse) == 0 && w.emitted && w.sink.AttachSource(text) {
			return nil
		}
		w.pendingProse = append(w.pendingProse, sec)
		return nil

	case spec.SectionPromo:
		w.flushProse()
		w.pending = ""
		w.sink.Promo(sec)
		w.emitted = true
		return nil

	case spec.SectionHero:
		// A beat of its own, like a KPI row and unlike a callout: a hero fills
		// the frame, so merging it into a run of prose would put a headline
		// under a heading and two ideas on one surface.
		w.flushProse()
		// It consumes a pending level-2 heading without using it, for the
		// reason Hero takes no title: leaving it would title the *next* beat
		// with a heading written for this one.
		w.pending = ""
		w.sink.Hero(sec)
		w.emitted = true
		return nil

	case spec.SectionKeyValue:
		w.flushProse()
		w.sink.Facts(w.title(), sec)
		w.emitted = true
		return nil

	case spec.SectionKPIRow:
		w.flushProse()
		w.sink.KPI(w.title(), sec)
		w.emitted = true
		return nil

	case spec.SectionChart:
		w.flushProse()
		// A chart's own title wins, then the section's, and only then the
		// heading above it. The order matters beyond precedence: resolving the
		// heading has a side effect — it consumes a pending level-2 title — so
		// a chart that names itself leaves that heading for the next beat.
		// That is the deck's behaviour and it is load-bearing, which is why the
		// precedence lives here rather than in each renderer.
		title := ""
		if sec.Chart != nil {
			title = strings.TrimSpace(sec.Chart.Title)
		}
		if title == "" {
			title = strings.TrimSpace(sec.Title)
		}
		if title == "" {
			title = w.title()
		}
		if err := w.sink.Chart(title, sec); err != nil {
			return err
		}
		w.emitted = true
		return nil

	case spec.SectionTable:
		w.flushProse()
		if err := w.sink.Table(w.title(), sec.Table()); err != nil {
			return err
		}
		w.emitted = true
		return nil

	case spec.SectionPageBreak, spec.SectionSpacer:
		// A page break is a document instruction. A projected renderer already
		// breaks between ideas, so it flushes what is buffered and does nothing
		// else — an empty slide in the middle of a deck is worse than a missing
		// one, and an empty four seconds in a video is worse than either.
		w.flushProse()
		return nil
	}
	return nil
}

func (w *walker) flushProse() {
	if len(w.pendingProse) == 0 {
		return
	}
	sections := w.pendingProse
	w.pendingProse = nil
	w.sink.Prose(w.title(), sections)
	w.emitted = true
}

// LeadSentences is what survives onto the surface: whole sentences from the
// front of the paragraph, as many as the caller's fits function accepts.
//
// Sentences rather than a character count, because a line cut mid-clause reads
// as a mistake while a line that is the paragraph's first sentence reads as a
// summary — and because the rest of the paragraph is in the notes, so nothing
// is lost either way.
//
// The caller supplies fits because only it knows its own box. It is also the
// caller's job to truncate the result: a single sentence longer than the budget
// comes back whole from here, which is the one case where there is no good
// answer and the answer has to be a visible ellipsis.
func LeadSentences(text string, fits func(candidate string) bool) string {
	sentences := SplitSentences(text)
	if len(sentences) == 0 {
		return ""
	}
	out := sentences[0]
	for _, s := range sentences[1:] {
		candidate := out + " " + s
		if !fits(candidate) {
			break
		}
		out = candidate
	}
	return out
}

// SplitSentences breaks on sentence-ending punctuation followed by a space.
//
// It is deliberately naive. The alternative — an abbreviation list — is a
// language-specific asset this renderer has no business carrying, and the cost
// of being wrong is a line that ends after "Rp 3,86 mil." instead of at the end
// of the clause. The full text is in the notes either way.
func SplitSentences(text string) []string {
	var (
		out   []string
		start int
	)
	runes := []rune(strings.TrimSpace(text))
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '.', '!', '?':
			if i+1 < len(runes) && runes[i+1] != ' ' {
				continue
			}
			s := strings.TrimSpace(string(runes[start : i+1]))
			if s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(string(runes[min(start, len(runes)):])); rest != "" {
		out = append(out, rest)
	}
	return out
}

// title is the title the next beat takes: an explicit level-2 heading if one is
// pending, otherwise the section it sits under, otherwise the document's own
// title.
//
// The last fallback is not cosmetic. A spec with no headings at all — the shape
// the model uses for "just give me the data" — produced slides with an empty
// title band: a 22mm hole at the top of every slide, and, on a table that runs
// across a dozen of them, nowhere for the "(cont.)" marker to go. A reader
// looking at slide 9 of that deck had no way to tell it continued slide 8.
func (w *walker) title() string {
	if w.pending != "" {
		t := w.pending
		w.pending = ""
		return t
	}
	if w.section != "" {
		return w.section
	}
	return w.docTitle
}
