package pptx

import (
	"strings"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Turning a document into a deck.
//
// The spec is a flow — a document is a single column that runs until it stops —
// and a deck is not. So this file is where the flow is cut into slides, and
// every decision in it is about where the cuts go.
//
// Three rules decide the shape:
//
//  1. One idea per slide. A KPI row, a chart, a table and a block of prose are
//     four slides, never one slide with four things on it, because the deck is
//     projected and the reader is three metres away.
//  2. The prose goes in the notes. A paragraph the model wrote is three or four
//     sentences of explanation; on a slide it is a wall of text nobody reads,
//     and in the speaker notes it is what the presenter says. What survives on
//     the slide is its lead sentence, as a bullet. This is the single change
//     that makes a generated deck feel authored rather than dumped.
//  3. Nothing is clipped silently. There is no layout engine to ask, so every
//     block is measured against the deck's own metrics before it is placed, and
//     anything that does not fit continues onto another slide that says so.

type slideKind int

const (
	kindCover slideKind = iota
	kindDivider
	kindKPI
	kindChart
	kindTable
	kindBullets
	kindFacts
	kindClosing
)

// slide is one rendered slide.
//
// Every payload field for every kind lives on this one struct rather than in a
// union, for the same reason spec.Section does: the shapes are small, the
// dispatch is one switch, and a kind that reads a field another kind wrote is a
// compile-time non-event rather than a type assertion.
type slide struct {
	kind  slideKind
	title string

	// subtitle is the cover's strapline, and a chart's or a table's caption.
	subtitle string

	// period, facts and confidentiality are the cover's and the closing
	// slide's furniture.
	period          string
	facts           [][2]string
	confidentiality string

	items   []spec.Item // kpi
	bullets []bullet
	callout *calloutBox
	table   *tableModel
	chart   *chartModel

	// notes is the speaker-notes text: the prose this slide is the headline of.
	notes string

	// continued marks a slide carrying the overflow of the one before it.
	continued bool
}

type bullet struct {
	text string

	// lines is what the text measured to, so the packer does not measure twice.
	lines int
}

type calloutBox struct {
	tone  string
	title string
	text  string
}

// builder walks the spec and accumulates slides.
type builder struct {
	r *renderer

	slides []slide

	// section is the current level-1 heading, used to title the slides under
	// it. A KPI row three sections into a report is not titled "KPI row".
	section string

	// pending is the title a level-2 heading set for the next slide, and the
	// prose waiting to become one.
	pending      string
	pendingProse []spec.Section
}

func (r *renderer) buildSlides() error {
	b := &builder{r: r}

	if cover := r.doc.Cover(); cover != nil {
		b.cover(*cover)
	}

	for _, sec := range r.sections {
		if err := b.section2Slides(sec); err != nil {
			return err
		}
	}
	b.flushProse()
	b.closing()

	r.slides = b.slides
	return nil
}

func (b *builder) section2Slides(sec spec.Section) error {
	switch sec.Type {
	case spec.SectionCover:
		return nil // drawn first, or ignored in v1

	case spec.SectionHeading:
		b.flushProse()
		if sec.Level == 2 {
			// A level-2 heading titles the next slide rather than taking a
			// slide of its own. A divider for every sub-heading turns a
			// ten-slide deck into twenty-five, half of them one word long.
			b.pending = strings.TrimSpace(sec.Text)
			return nil
		}
		b.section = strings.TrimSpace(sec.Text)
		b.pending = ""
		b.slides = append(b.slides, slide{kind: kindDivider, title: b.section})
		return nil

	case spec.SectionParagraph, spec.SectionCallout:
		b.pendingProse = append(b.pendingProse, sec)
		return nil

	case spec.SectionFootnote:
		// A footnote annotates whatever came before it — a table's source, a
		// chart's methodology — so it goes into that slide's notes rather than
		// onto a slide of its own. With nothing before it, it is prose.
		text := strings.TrimSpace(sec.Text)
		if text == "" {
			return nil
		}
		if len(b.pendingProse) == 0 && len(b.slides) > 0 {
			b.appendNotes(len(b.slides)-1, b.r.words.Source+": "+text)
			return nil
		}
		b.pendingProse = append(b.pendingProse, sec)
		return nil

	case spec.SectionKeyValue:
		b.flushProse()
		b.facts(sec)
		return nil

	case spec.SectionKPIRow:
		b.flushProse()
		b.kpi(sec)
		return nil

	case spec.SectionChart:
		b.flushProse()
		return b.chart(sec)

	case spec.SectionTable:
		b.flushProse()
		return b.table(sec.Table())

	case spec.SectionPageBreak, spec.SectionSpacer:
		// A page break is a document instruction. The deck already breaks
		// between ideas, so it flushes what is buffered and does nothing else —
		// an empty slide in the middle of a deck is worse than a missing one.
		b.flushProse()
		return nil
	}
	return nil
}

// slideTitle is the title the next content slide takes: an explicit level-2
// heading if one is pending, otherwise the section it sits under, otherwise the
// document's own title.
//
// The last fallback is not cosmetic. A spec with no headings at all — the shape
// the model uses for "just give me the data" — produced slides with an empty
// title band: a 22mm hole at the top of every slide, and, on a table that runs
// across a dozen of them, nowhere for the "(cont.)" marker to go. A reader
// looking at slide 9 of that deck had no way to tell it continued slide 8.
func (b *builder) slideTitle() string {
	if b.pending != "" {
		t := b.pending
		b.pending = ""
		return t
	}
	return firstNonEmpty(b.section, b.r.title)
}

func (b *builder) appendNotes(i int, text string) {
	if text == "" {
		return
	}
	if b.slides[i].notes != "" {
		b.slides[i].notes += "\n\n"
	}
	b.slides[i].notes += text
}

func (b *builder) cover(sec spec.Section) {
	r := b.r
	title := firstNonEmpty(sec.Text, sec.Title, r.title, "Report")

	facts := make([][2]string, 0, 3)
	if v := strings.TrimSpace(sec.PreparedFor); v != "" {
		facts = append(facts, [2]string{r.words.PreparedFor, v})
	}
	if v := firstNonEmpty(sec.PreparedBy, r.opts.Brand.Name); v != "" {
		facts = append(facts, [2]string{r.words.PreparedBy, v})
	}
	facts = append(facts, [2]string{r.words.Generated, format.DateTime(r.genAt, r.fmt)})

	b.slides = append(b.slides, slide{
		kind:            kindCover,
		title:           title,
		subtitle:        strings.TrimSpace(sec.Subtitle),
		period:          strings.TrimSpace(sec.Period),
		facts:           facts,
		confidentiality: r.confid,
	})
}

func (b *builder) closing() {
	r := b.r
	facts := [][2]string{{r.words.Generated, format.DateTime(r.genAt, r.fmt)}}
	if name := strings.TrimSpace(r.opts.Brand.Name); name != "" {
		facts = append([][2]string{{r.words.PreparedBy, name}}, facts...)
	}
	b.slides = append(b.slides, slide{
		kind:            kindClosing,
		title:           r.title,
		facts:           facts,
		confidentiality: r.confid,
	})
}

func (b *builder) kpi(sec spec.Section) {
	items := sec.Items
	if len(items) > 4 {
		items = items[:4]
	}
	b.slides = append(b.slides, slide{
		kind:  kindKPI,
		title: b.slideTitle(),
		items: items,
	})
}

// facts is a key_value block: the invoice or agreement header, as a two-column
// list.
//
// The value may wrap. It has to: the first thing a key_value block carries in
// practice is a billing address, and "Meridian Logistics Pte Ltd, 8 Marina View
// #22-01, Asia…" truncated to one line is an invoice missing the address it is
// addressed to. So rows are packed by their measured height rather than counted
// against a fixed rows-per-slide, and a block that does not fit continues.
func (b *builder) facts(sec spec.Section) {
	title := b.slideTitle()

	rows := make([][2]string, 0, len(sec.Items))
	for _, item := range sec.Items {
		rows = append(rows, [2]string{
			item.KeyText(),
			b.r.cellText(item.ValueCell(), format.KindText, b.r.fmt),
		})
	}

	avail := bodyHeight()
	used := 0.0
	var page [][2]string
	flush := func() {
		if len(page) > 0 {
			b.slides = append(b.slides, slide{
				kind:      kindFacts,
				title:     title,
				facts:     page,
				continued: len(b.slides) > 0 && b.slides[len(b.slides)-1].kind == kindFacts,
			})
			page = nil
			used = 0
		}
	}
	for _, row := range rows {
		h := factRowHeight(row)
		if used+h > avail && len(page) > 0 {
			flush()
		}
		page = append(page, row)
		used += h
	}
	flush()
}

// maxFactLines is how far a fact's value may wrap. Three lines holds a postal
// address, which is what this block is for.
const maxFactLines = 3

// factLabelShare is the width of the label column, as a fraction of the
// measure.
const factLabelShare = 0.34

func factRowHeight(row [2]string) float64 {
	valueW := contentWidth() * (1 - factLabelShare)
	lines := max(1, min(linesIn(row[1], theme.FontBody, measure.Bold, deckType.Body, valueW), maxFactLines))
	return float64(lines)*measure.LineHeightMM(deckType.Body)*bodyLeading + theme.Spacing.SM
}

// flushProse turns the buffered paragraphs and callouts into slides.
//
// The slide keeps a lead — the opening sentence or two of each paragraph, as a
// bullet — and the notes keep the paragraph entire. Nothing is dropped; what
// changes is which surface carries it.
func (b *builder) flushProse() {
	if len(b.pendingProse) == 0 {
		return
	}
	sections := b.pendingProse
	b.pendingProse = nil
	title := b.slideTitle()

	var (
		bullets  []bullet
		callouts []*calloutBox
		notes    []string
	)
	for _, sec := range sections {
		text := strings.TrimSpace(sec.Text)
		switch sec.Type {
		case spec.SectionCallout:
			callouts = append(callouts, &calloutBox{
				tone:  sec.Tone,
				title: strings.TrimSpace(sec.Title),
				text:  text,
			})
			if t := strings.TrimSpace(sec.Title); t != "" && text != "" {
				notes = append(notes, t+" — "+text)
			} else {
				notes = append(notes, firstNonEmpty(t, text))
			}
		case spec.SectionFootnote:
			notes = append(notes, b.r.words.Source+": "+text)
		default:
			if text == "" {
				continue
			}
			lead := leadSentences(text, bulletWidth(), deckType.Body, maxBulletLines)
			bullets = append(bullets, bullet{
				text:  lead,
				lines: linesIn(lead, theme.FontBody, measure.Regular, deckType.Body, bulletWidth()),
			})
			notes = append(notes, text)
		}
	}

	pages := packBullets(bullets, callouts)
	for i, p := range pages {
		b.slides = append(b.slides, slide{
			kind:      kindBullets,
			title:     title,
			bullets:   p.bullets,
			callout:   p.callout,
			continued: i > 0,
		})
	}
	// The prose belongs to the first slide of the run: it is the notes for the
	// idea, and the continuation slides are the same idea still being made.
	if len(pages) > 0 {
		b.appendNotes(len(b.slides)-len(pages), strings.Join(notes, "\n\n"))
	}
}

// maxBulletLines is how far one bullet may run before it stops being a bullet.
// Three lines of 18pt across the measure is about forty words, which is already
// past what anyone reads off a projected slide.
const maxBulletLines = 3

// bulletWidth is the measure a bullet is set across: the content width less the
// hanging indent the bullet character occupies.
func bulletWidth() float64 { return contentWidth() - 8 }

// bulletPage is one slide's worth of the prose buffer.
type bulletPage struct {
	bullets []bullet
	callout *calloutBox
}

// packBullets fills slides until the body area is full, then starts another.
//
// The height of a callout is known before it is drawn (calloutHeight measures
// its own text), so the packer can decide whether the callout fits beside the
// bullets already placed or has to start the next slide. A callout half off the
// bottom of a slide is the failure this exists to prevent.
func packBullets(bullets []bullet, callouts []*calloutBox) []bulletPage {
	if len(bullets) == 0 && len(callouts) == 0 {
		return nil
	}

	avail := bodyHeight()
	lineH := measure.LineHeightMM(deckType.Body) * bodyLeading

	var (
		pages []bulletPage
		cur   bulletPage
		used  float64
	)
	flush := func() {
		if len(cur.bullets) > 0 || cur.callout != nil {
			pages = append(pages, cur)
		}
		cur, used = bulletPage{}, 0
	}

	for _, bl := range bullets {
		h := float64(max(bl.lines, 1))*lineH + theme.Spacing.MD
		if used+h > avail && len(cur.bullets) > 0 {
			flush()
		}
		cur.bullets = append(cur.bullets, bl)
		used += h
	}
	for _, c := range callouts {
		h := calloutHeight(c) + theme.Spacing.MD
		if cur.callout != nil || used+h > avail {
			flush()
		}
		cur.callout = c
		used += h
	}
	flush()
	return pages
}

// leadSentences is what survives onto the slide: whole sentences from the front
// of the paragraph, as many as fit in the bullet's line budget.
//
// Sentences rather than a character count, because a bullet cut mid-clause
// reads as a mistake while a bullet that is the paragraph's first sentence
// reads as a summary — and because the rest of the paragraph is in the notes,
// so nothing is lost either way. A single sentence longer than the budget is
// truncated with a visible ellipsis, which is the one case where there is no
// good answer.
func leadSentences(text string, width, size float64, maxLines int) string {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return ""
	}
	out := sentences[0]
	for _, s := range sentences[1:] {
		candidate := out + " " + s
		if !wordsFit(candidate, size, width, maxLines, false) {
			break
		}
		out = candidate
	}
	return fitLines(out, theme.FontBody, measure.Regular, size, width, maxLines)
}

// splitSentences breaks on sentence-ending punctuation followed by a space.
//
// It is deliberately naive. The alternative — an abbreviation list — is a
// language-specific asset this renderer has no business carrying, and the cost
// of being wrong is a bullet that ends after "Rp 3,86 mil." instead of at the
// end of the clause. The full text is in the notes either way.
func splitSentences(text string) []string {
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

func chunk[T any](items []T, size int) [][]T {
	if size <= 0 {
		size = 1
	}
	var out [][]T
	for i := 0; i < len(items); i += size {
		out = append(out, items[i:min(i+size, len(items))])
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
