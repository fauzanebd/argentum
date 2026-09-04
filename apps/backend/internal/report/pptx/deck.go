package pptx

import (
	"strings"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/flow"
	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Turning a document into a deck.
//
// **Where the cuts go is no longer decided here.** internal/report/flow walks
// the spec and says what each beat is and what it is called; this file says how
// much of a beat fits on a slide and what the slide looks like. The split is
// T-V1's, and the reason is the reason for every other extraction in this tree:
// the video renderer projects the same spec, and a table that continues onto a
// second slide has to continue onto a second scene or the same report has
// quietly become two reports.
//
// The three rules that decide the shape now live in flow's package comment. The
// one this file still owns is the third: nothing is clipped silently. There is
// no layout engine on the other side, so every block is measured against the
// deck's own metrics before it is placed, and anything that does not fit
// continues onto another slide that says so.

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

// builder turns the beats flow emits into slides. It is a flow.Sink.
type builder struct {
	r *renderer

	slides []slide
}

func (r *renderer) buildSlides() error {
	b := &builder{r: r}
	if err := flow.Walk(r.sections, r.doc.Cover(), r.title, b); err != nil {
		return err
	}
	r.slides = b.slides
	return nil
}

// Divider opens a level-1 section with a slide of its own.
func (b *builder) Divider(title string) {
	b.slides = append(b.slides, slide{kind: kindDivider, title: title})
}

// AttachSource puts a footnote into the previous slide's notes.
func (b *builder) AttachSource(text string) bool {
	if len(b.slides) == 0 {
		return false
	}
	b.appendNotes(len(b.slides)-1, b.r.words.Source+": "+text)
	return true
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

// Cover draws the document's cover, when it has one.
func (b *builder) Cover(sec spec.Section) {
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

// Closing is the last slide.
func (b *builder) Closing() {
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

// KPI is a kpi_row, at most four cards wide.
func (b *builder) KPI(title string, sec spec.Section) {
	items := sec.Items
	if len(items) > 4 {
		items = items[:4]
	}
	b.slides = append(b.slides, slide{
		kind:  kindKPI,
		title: title,
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
func (b *builder) Facts(title string, sec spec.Section) {
	rows := make([][2]string, 0, len(sec.Items))
	for _, item := range sec.Items {
		rows = append(rows, [2]string{
			item.KeyText(),
			canvas.CellText(item.ValueCell(), format.KindText, b.r.fmt),
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

// The fact block's wrapping budget and label column are canvas's since T-V1, so
// the deck and the video split a long key_value block in the same place.
const (
	maxFactLines   = canvas.MaxFactLines
	factLabelShare = canvas.FactLabelShare
)

func factRowHeight(row [2]string) float64 { return surface.FactRowHeight(row[1]) }

// Prose turns a run of paragraphs and callouts into slides.
//
// The slide keeps a lead — the opening sentence or two of each paragraph, as a
// bullet — and the notes keep the paragraph entire. Nothing is dropped; what
// changes is which surface carries it.
func (b *builder) Prose(title string, sections []spec.Section) {
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
// of the paragraph, as many as fit in the bullet's line budget, truncated with a
// visible ellipsis when even the first one does not.
//
// The sentence splitting and the take-while are flow.LeadSentences, shared with
// the video renderer since T-V1 — the same paragraph has to reduce to the same
// lead in both, or the deck and the video are summarising the report
// differently. What stays here is the box it has to fit in, which is the deck's
// alone.
func leadSentences(text string, width, size float64, maxLines int) string {
	lead := flow.LeadSentences(text, func(candidate string) bool {
		return wordsFit(candidate, size, width, maxLines, false)
	})
	return fitLines(lead, theme.FontBody, measure.Regular, size, width, maxLines)
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
