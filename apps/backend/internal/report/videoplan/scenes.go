package videoplan

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/chart"
	"github.com/fauzanebd/argentum/internal/report/flow"
	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The builder is a flow.Sink: it receives beats and turns each into one or more
// scenes.
//
// Where it differs from the deck, and why:
//
//   - **One paragraph per scene, not a bulleted list.** The deck packs several
//     leads onto a slide because a slide is a static thing a presenter talks
//     over. A frame is on screen for as long as its own text takes to read, so
//     packing four paragraphs onto one either rushes three of them or holds the
//     fourth for half a minute. One idea, one beat, its own duration.
//   - **A callout is its own scene.** Same reason, plus the obvious one: a
//     callout is the finding the reader must not miss, and it earns a frame.
//   - **Everything else is the deck's.** The cover, the dividers, the KPI cards,
//     the table paging and the closing are the same projection, because they are
//     the same document.

// maxTitleLines is what the title band holds: two lines of H1 plus its leading.
// A third is truncated rather than allowed to push the content down, because a
// title that long is the problem, not the layout.
const maxTitleLines = 2

// maxStatementLines is how far one statement's copy may run. Five lines of H2
// across the measure is roughly sixty words — already at the ceiling of what a
// viewer reads off a frame before it changes.
const maxStatementLines = 5

// maxQuoteLines is a callout's budget. Shorter than a statement: a callout that
// runs to five lines is a paragraph wearing a border.
const maxQuoteLines = 4

// Cover is the document's cover.
func (b *builder) Cover(sec spec.Section) {
	title := firstNonEmpty(sec.Text, sec.Title, b.title, "Report")

	facts := make([]Fact, 0, 3)
	if v := strings.TrimSpace(sec.PreparedFor); v != "" {
		facts = append(facts, b.fact(b.words.PreparedFor, v))
	}
	if v := firstNonEmpty(sec.PreparedBy, b.opts.Brand.Name); v != "" {
		facts = append(facts, b.fact(b.words.PreparedBy, v))
	}
	facts = append(facts, b.fact(b.words.Generated, format.DateTime(b.genAt, b.fmt)))

	b.scenes = append(b.scenes, Scene{
		Kind:     KindCover,
		Frames:   frames(CoverSeconds),
		Title:    b.lines(title, measure.Bold, canvas.Type.Display, canvas.ContentWidth(), maxTitleLines),
		Subtitle: b.lines(sec.Subtitle, measure.Regular, canvas.Type.H2, canvas.ContentWidth(), 2),
		Period:   strings.TrimSpace(sec.Period),
		Facts:    facts,
	})
}

// Divider opens a level-1 section.
func (b *builder) Divider(title string) {
	b.scenes = append(b.scenes, Scene{
		Kind:   KindSection,
		Frames: frames(DividerSeconds),
		Title:  b.lines(title, measure.Bold, canvas.Type.Display, canvas.ContentWidth(), maxTitleLines),
	})
}

// Closing is the last scene.
func (b *builder) Closing() {
	facts := []Fact{}
	if name := strings.TrimSpace(b.opts.Brand.Name); name != "" {
		facts = append(facts, b.fact(b.words.PreparedBy, name))
	}
	facts = append(facts, b.fact(b.words.Generated, format.DateTime(b.genAt, b.fmt)))

	b.scenes = append(b.scenes, Scene{
		Kind:   KindClosing,
		Frames: frames(ClosingSeconds),
		Title:  b.lines(b.title, measure.Bold, canvas.Type.Display, canvas.ContentWidth(), maxTitleLines),
		Facts:  facts,
	})
}

// AttachSource puts a footnote into the previous scene's notes.
func (b *builder) AttachSource(text string) bool {
	if len(b.scenes) == 0 {
		return false
	}
	b.appendNotes(len(b.scenes)-1, b.words.Source+": "+text)
	return true
}

func (b *builder) appendNotes(i int, text string) {
	if text == "" {
		return
	}
	if b.scenes[i].Notes != "" {
		b.scenes[i].Notes += "\n\n"
	}
	b.scenes[i].Notes += text
}

// Prose turns a run of paragraphs and callouts into one scene each.
//
// The scene keeps a lead — the opening sentence or two, which is what a viewer
// can read in the time the frame is up — and the notes keep the paragraph
// entire, for T-V4's player. Nothing is dropped; what changes is which surface
// carries it. The whole run's prose is attached to the first scene of the run,
// because the run is one idea still being made.
func (b *builder) Prose(title string, sections []spec.Section) {
	titleLines := b.lines(title, measure.Bold, canvas.Type.H1, canvas.ContentWidth(), maxTitleLines)
	first := len(b.scenes)
	var notes []string

	for _, sec := range sections {
		text := strings.TrimSpace(sec.Text)
		switch sec.Type {
		case spec.SectionCallout:
			heading := strings.TrimSpace(sec.Title)
			if heading == "" && text == "" {
				continue
			}
			lines := b.lines(text, measure.Regular, canvas.Type.H2, canvas.ContentWidth(), maxQuoteLines)
			b.scenes = append(b.scenes, Scene{
				Kind:     KindQuote,
				Frames:   readingFrames(heading, strings.Join(lines, " ")),
				Title:    titleLines,
				Subtitle: b.lines(heading, measure.Bold, canvas.Type.H1, canvas.ContentWidth(), 1),
				Lines:    lines,
				Tone:     tone(sec.Tone),
			})
			if heading != "" && text != "" {
				notes = append(notes, heading+" — "+text)
			} else {
				notes = append(notes, firstNonEmpty(heading, text))
			}

		case spec.SectionFootnote:
			notes = append(notes, b.words.Source+": "+text)

		default:
			if text == "" {
				continue
			}
			lead := flow.LeadSentences(text, func(candidate string) bool {
				return canvas.LinesIn(candidate, theme.FontBody, measure.Regular,
					canvas.Type.H2, canvas.ContentWidth()) <= maxStatementLines
			})
			lines := b.lines(lead, measure.Regular, canvas.Type.H2, canvas.ContentWidth(), maxStatementLines)
			if len(lines) == 0 {
				continue
			}
			b.scenes = append(b.scenes, Scene{
				Kind:   KindStatement,
				Frames: readingFrames(strings.Join(lines, " ")),
				Title:  titleLines,
				Lines:  lines,
			})
			notes = append(notes, text)
		}
	}

	if len(b.scenes) > first {
		b.appendNotes(first, strings.Join(notes, "\n\n"))
	}
}

// Facts is a key_value block, packed by measured height and continued when it
// does not fit — canvas.FactRowHeight, the same packing the deck uses.
func (b *builder) Facts(title string, sec spec.Section) {
	titleLines := b.lines(title, measure.Bold, canvas.Type.H1, canvas.ContentWidth(), maxTitleLines)

	rows := make([]Fact, 0, len(sec.Items))
	for _, item := range sec.Items {
		rows = append(rows, b.fact(item.KeyText(), canvas.CellText(item.ValueCell(), format.KindText, b.fmt)))
	}

	avail := canvas.BodyHeight()
	used := 0.0
	var page []Fact
	continued := false
	flush := func() {
		if len(page) == 0 {
			return
		}
		b.scenes = append(b.scenes, Scene{
			Kind:      KindTable,
			Frames:    factFrames(page),
			Title:     titleLines,
			Facts:     page,
			Continued: continued,
		})
		page, used, continued = nil, 0, true
	}
	for _, row := range rows {
		h := canvas.FactRowHeight(strings.Join(row.Value, " "))
		if used+h > avail && len(page) > 0 {
			flush()
		}
		page = append(page, row)
		used += h
	}
	flush()
}

// KPI is a kpi_row, at most four cards wide — the deck's cap, for the deck's
// reason: a fifth card is narrower than the number on it.
func (b *builder) KPI(title string, sec spec.Section) {
	items := sec.Items
	if len(items) > 4 {
		items = items[:4]
	}
	if len(items) == 0 {
		return
	}

	// KPI values are compact by default: "Rp 3,86 Miliar" fits a card,
	// "Rp 3.863.405.700" does not — and the exact figure belongs in the table
	// the card is summarising, not on the card.
	valueFmt := b.fmt
	valueFmt.Compact = true

	cards := make([]KPI, 0, len(items))
	for _, item := range items {
		card := KPI{
			Label: item.KeyText(),
			Value: canvas.CellText(item.ValueCell(), format.KindNumber, valueFmt),
		}
		if item.DeltaPct != nil {
			deltaOpts := b.fmt
			deltaOpts.Decimals = 1
			card.Delta = format.Signed(*item.DeltaPct, deltaOpts)
			card.Rising = item.Rising()
			card.Good = item.GoodDirection()
		}
		cards = append(cards, card)
	}

	b.scenes = append(b.scenes, Scene{
		Kind:   KindKPI,
		Frames: kpiFrames(cards),
		Title:  b.lines(title, measure.Bold, canvas.Type.H1, canvas.ContentWidth(), maxTitleLines),
		KPIs:   cards,
	})
}

// Chart draws the chart and puts it on a scene of its own.
//
// The image is internal/report/chart's, at the same width and aspect the deck
// uses, so the same spec produces the same picture in all three formats. What
// the video adds is a mask over it (locked decision 6) — the pixels are never
// redrawn.
func (b *builder) Chart(title string, sec spec.Section) error {
	width := canvas.ContentWidth()
	height := math.Min(canvas.MaxChartHeight(), width*canvas.ChartAspect)

	res, err := chart.Render(sec.Chart, chart.Options{
		WidthMM:  width,
		HeightMM: height,
		Format:   b.fmt,
	})
	if err != nil {
		return fmt.Errorf("videoplan: chart: %w", err)
	}

	caption := strings.TrimSpace(sec.Caption)
	if res.Note != "" {
		caption = strings.TrimSpace(caption + " " + res.Note)
	}
	captionLines := b.lines(caption, measure.Regular, canvas.Type.Caption, canvas.ContentWidth(), 2)

	b.scenes = append(b.scenes, Scene{
		Kind:    KindChart,
		Frames:  chartFrames(title, captionLines),
		Title:   b.lines(title, measure.Bold, canvas.Type.H1, canvas.ContentWidth(), maxTitleLines),
		Caption: captionLines,
		Chart: &Chart{
			DataURI: pngDataURI(res.PNG),
			Width:   canvas.Px(res.WidthMM),
			Height:  canvas.Px(res.HeightMM),
			Reveal:  reveal(sec.Chart),
		},
	})
	return nil
}

// Table pages a table across scenes, using the surface's own row count so a
// table breaks in the same place in the deck and in the video.
func (b *builder) Table(title string, t *spec.Table) error {
	if t == nil || len(t.Columns) == 0 {
		return nil
	}
	m := canvas.BuildTable(t, b.fmt)
	titleLines := b.lines(title, measure.Bold, canvas.Type.H1, canvas.ContentWidth(), maxTitleLines)
	captionLines := b.lines(m.Caption, measure.Regular, canvas.Type.Caption, canvas.ContentWidth(), 2)

	widths := make([]int, len(m.Widths))
	for i, w := range m.Widths {
		widths[i] = canvas.Px(w)
	}

	rows := m.RowsPerSurface()
	pages := chunk(m.Rows, rows)
	if len(pages) == 0 {
		pages = [][][]string{nil}
	}

	for i, page := range pages {
		table := &Table{
			Header:       m.Header,
			Aligns:       m.Aligns,
			Widths:       widths,
			Rows:         page,
			FontSize:     canvas.PtPx(m.Size),
			RowHeight:    canvas.Px(m.RowH),
			HeaderHeight: canvas.Px(m.HeaderH),
		}
		// The total row belongs on the last scene of the run, under the last
		// rows it totals. Repeating it on every continuation would state the
		// same sum three times against three different subsets of the data.
		if i == len(pages)-1 {
			table.Total = m.Total
		}
		b.scenes = append(b.scenes, Scene{
			Kind:      KindTable,
			Frames:    tableFrames(table),
			Title:     titleLines,
			Caption:   captionLines,
			Table:     table,
			Continued: i > 0,
		})
	}
	return nil
}

// fact wraps a value to its column, so the renderer draws lines rather than
// breaking an address wherever the box happens to end.
func (b *builder) fact(label, value string) Fact {
	width := canvas.ContentWidth() * (1 - canvas.FactLabelShare)
	return Fact{
		Label: strings.TrimSpace(label),
		Value: b.lines(value, measure.Bold, canvas.Type.Body, width, canvas.MaxFactLines),
	}
}

// lines fits a string to its box and returns the lines it will occupy.
//
// Fit first, then wrap: truncation adds the ellipsis that says a string was cut,
// and wrapping the truncated result is what produces the lines the renderer
// draws. Doing it the other way round would wrap text that is about to be
// thrown away and put the ellipsis in the middle of the box.
func (b *builder) lines(s string, style measure.Style, size, width float64, maxLines int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return canvas.Wrap(canvas.FitLines(s, theme.FontBody, style, size, width, maxLines),
		theme.FontBody, style, size, width)
}

// reveal picks how the mask over a chart moves.
//
// A wipe for anything with a horizontal axis, because that is the direction the
// data runs; a radial sweep for a pie or a donut, because they have no left; and
// nothing for a sparkline, which is too small for the movement to read as
// anything but a flicker.
func reveal(c *spec.Chart) string {
	if c == nil {
		return RevealNone
	}
	switch c.Type {
	case spec.ChartPie, spec.ChartDonut:
		return RevealSweep
	case spec.ChartSparkline:
		return RevealNone
	default:
		return RevealWipe
	}
}

func tone(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case spec.ToneWarn:
		return spec.ToneWarn
	case spec.ToneGood:
		return spec.ToneGood
	default:
		return spec.ToneInfo
	}
}

func pngDataURI(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
}

// factFrames is the time a fact block is on screen: its rows are scanned rather
// than read, but a fifteen-row invoice header is not a three-row one.
func factFrames(rows []Fact) int {
	s := SceneLeadInSeconds + float64(len(rows))*TableRowSeconds
	for _, r := range rows {
		s += float64(words(r.Label)+wordsIn(r.Value)) / (ReadWordsPerSecond * 3)
	}
	return frames(clamp(s))
}

// kpiFrames is the time a KPI row is on screen. A card is a label, a number and
// maybe a delta — read quickly, but four of them is four readings.
func kpiFrames(cards []KPI) int {
	s := SceneLeadInSeconds + float64(len(cards))*KPICardSeconds
	for _, c := range cards {
		s += float64(words(c.Label)+words(c.Value)) / (ReadWordsPerSecond * 2)
	}
	return frames(clamp(s))
}

// tableFrames is the time a table is on screen: a per-row scan cost plus the
// header, which is read once.
func tableFrames(t *Table) int {
	s := SceneLeadInSeconds + float64(len(t.Rows))*TableRowSeconds
	s += float64(wordsIn(t.Header)) / (ReadWordsPerSecond * 2)
	if len(t.Total) > 0 {
		s += TableRowSeconds
	}
	return frames(clamp(s))
}

// chartFrames is the reveal, then the time it takes to read what it uncovered.
func chartFrames(title string, caption []string) int {
	s := ChartRevealSeconds + readSeconds(title, strings.Join(caption, " "))
	if s < ChartRevealSeconds+MinSceneSeconds {
		s = ChartRevealSeconds + MinSceneSeconds
	}
	return frames(math.Min(s, MaxSceneSeconds))
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
