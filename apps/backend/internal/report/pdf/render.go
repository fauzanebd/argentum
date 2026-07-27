// Package pdf renders a report spec into an A4 PDF.
//
// It replaces the renderer that lived in internal/tools/document, which was a
// title, a bold row per heading and a table whose columns were the 12-unit
// grid divided evenly. That document was correct and looked generated. This
// one has a cover, a running header, a numbered section hierarchy, KPI cards,
// typed and locale-formatted table cells, content-weighted columns and a
// footer that says which page you are on — all of it drawn from the same
// tokens.json the dashboard's CSS comes from, so the file a customer forwards
// looks like the product it came from.
//
// Layout rules that are not obvious from the code:
//
//   - The cover is page 1 and carries no running header, because a header that
//     repeats the title above the title is noise. maroto adds registered header
//     rows to whichever page is current, so the cover is rendered first, the
//     page is closed, and only then are the header and footer registered.
//   - Nothing that reads as a unit is split across a page: a KPI row, a
//     callout, a table header with no rows under it. Each checks that it fits
//     before it is emitted.
//   - Every measurement is taken from the same font metrics maroto renders
//     with (see measure.go), so a row is exactly as tall as its content.
package pdf

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Brand is the per-tenant identity on a document. T-R5 fills it from company
// settings; until then the zero value renders Argentum's own mark, which is
// why every field is optional and none of them is consulted twice.
type Brand struct {
	// Name is the legal entity the document belongs to. It goes on the cover
	// as "Prepared by" when the spec does not say, and into the PDF's Author
	// property, which is what a document-management system files it under.
	Name string

	// LogoPNG is a PNG the cover and the running header draw instead of the
	// wordmark. PNG only: JPEG artefacts around a logo's edges are exactly
	// where they are most visible.
	LogoPNG []byte

	// Confidentiality is the default label for documents that do not set one
	// ("Internal", "Confidential"). Empty means no label anywhere.
	Confidentiality string

	// FooterNote is a legal line some tenants must carry on every page.
	FooterNote string
}

// Options is everything the renderer needs that does not come from the model.
type Options struct {
	Brand Brand

	// Currency is the company's ISO 4217 default, used when the spec does not
	// name one. A document with no currency anywhere renders bare numbers
	// rather than inventing a symbol.
	Currency string

	// Locale overrides the locale derived from the currency.
	Locale string

	// Now is the fallback generated-at when the spec omits one. Zero means
	// time.Now(); tests set it to keep bytes stable.
	Now time.Time
}

// Render turns a document spec into PDF bytes.
func Render(doc *spec.Document, opts Options) ([]byte, error) {
	if doc == nil {
		return nil, fmt.Errorf("pdf: nil spec")
	}
	r, err := newRenderer(doc, opts)
	if err != nil {
		return nil, err
	}
	return r.run()
}

type renderer struct {
	m    core.Maroto
	doc  *spec.Document
	opts Options

	fmt      format.Options
	labels   labels
	title    string
	confid   string
	genAt    time.Time
	sections []spec.Section

	// h1 and h2 number the section hierarchy. They are renderer state rather
	// than spec state because a numbered heading is a rendering decision: the
	// same spec rendered as a deck (T-R4) numbers nothing.
	h1, h2 int

	// numbered is false for a document with one top-level section. Numbering
	// exists so a reader can navigate; "1. Invoice 2026-0417" above the only
	// heading in an invoice is bureaucracy, not navigation.
	numbered bool
}

func newRenderer(doc *spec.Document, opts Options) (*renderer, error) {
	r := &renderer{doc: doc, opts: opts}

	currency := doc.Currency
	if currency == "" {
		currency = opts.Currency
	}
	locale := doc.Locale
	if locale == "" {
		locale = opts.Locale
	}
	loc := format.LocaleForCurrency(currency)
	if locale != "" {
		loc = format.ParseLocale(locale)
	}
	r.fmt = format.Options{
		Locale:   loc,
		Currency: strings.ToUpper(strings.TrimSpace(currency)),
		Decimals: format.AutoDecimals,
	}
	r.labels = labelsFor(loc)

	r.title = strings.TrimSpace(doc.Title)
	r.sections = doc.Content.Sections

	// A spec with a bare content.table and no sections is the shape the model
	// uses for "just give me the data". It renders as a one-section document
	// rather than as a special case, so paging, zebra bands and column
	// weighting are the same code.
	if len(r.sections) == 0 && doc.Content.Table != nil {
		t := doc.Content.Table
		r.sections = []spec.Section{{
			Type:     spec.SectionTable,
			Columns:  t.Columns,
			Rows:     t.Rows,
			TotalRow: t.TotalRow,
			Caption:  t.Caption,
		}}
	}
	if len(r.sections) == 0 {
		return nil, fmt.Errorf("pdf: content.sections or content.table required")
	}

	tops := 0
	for _, sec := range r.sections {
		if sec.Type == spec.SectionHeading && sec.Level != 2 {
			tops++
		}
	}
	r.numbered = doc.V2() && tops >= 2

	genAt, explicit := doc.Generated()
	if !explicit && !opts.Now.IsZero() {
		genAt = opts.Now
	}
	r.genAt = genAt

	r.confid = strings.TrimSpace(opts.Brand.Confidentiality)
	if c := doc.Cover(); c != nil && strings.TrimSpace(c.Confidentiality) != "" {
		r.confid = strings.TrimSpace(c.Confidentiality)
	}

	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	r.m = maroto.New(cfg)
	return r, nil
}

func (r *renderer) config() (*entity.Config, error) {
	b, err := theme.ConfigBuilder()
	if err != nil {
		return nil, fmt.Errorf("pdf: theme: %w", err)
	}

	author := strings.TrimSpace(r.doc.Meta.Author)
	if author == "" {
		author = strings.TrimSpace(r.opts.Brand.Name)
	}
	title := r.title
	if title == "" {
		title = "Report"
	}

	b = b.WithTitle(title, true).
		WithCreator("Argentum", true).
		// The creation date is the spec's generated_at, not the clock. Two
		// runs of the same spec have to produce the same bytes — that is what
		// makes a golden test possible, and gofpdf writes this value straight
		// into /CreationDate.
		WithCreationDate(r.genAt)
	if author != "" {
		b = b.WithAuthor(author, true)
	}
	if s := strings.TrimSpace(r.doc.Meta.Subject); s != "" {
		b = b.WithSubject(s, true)
	}
	if k := strings.TrimSpace(r.doc.Meta.Keywords); k != "" {
		b = b.WithKeywords(k, true)
	}

	if r.doc.V2() {
		b = b.WithPageNumber(props.PageNumber{
			Pattern: r.labels.pageNumber,
			Place:   props.Bottom,
			Family:  theme.FontBody,
			Style:   fontstyle.Normal,
			Size:    theme.TypeScale.Caption,
			Color:   theme.ColorMuted.Props(),
		})
	}
	return b.Build(), nil
}

func (r *renderer) run() ([]byte, error) {
	if err := r.build(); err != nil {
		return nil, err
	}
	out, err := r.m.Generate()
	if err != nil {
		return nil, fmt.Errorf("pdf: generate: %w", err)
	}
	return out.GetBytes(), nil
}

// build lays the document out without rendering it. It is separate from run so
// tests can walk maroto's component tree — which is the only way to assert
// that a running header is on every page but the cover, since the rendered
// bytes encode text as subset glyph ids that nothing can grep.
func (r *renderer) build() error {
	cover := r.doc.Cover()

	if r.doc.V2() && cover != nil {
		r.renderCover(*cover)
		// Close the cover page before the header and footer exist, so neither
		// appears on it. maroto has no "start a new page" call; adding an
		// empty page flushes the current one, which is what this is.
		r.m.AddPages(page.New())
	}

	if r.doc.V2() {
		// Footer first: RegisterHeader's own fit check reads footerHeight, and
		// a header registered against a zero footer reserves the wrong space.
		if err := r.m.RegisterFooter(r.footerRows()...); err != nil {
			return fmt.Errorf("pdf: footer: %w", err)
		}
		if err := r.m.RegisterHeader(r.headerRows()...); err != nil {
			return fmt.Errorf("pdf: header: %w", err)
		}
	}

	for i, sec := range r.sections {
		if sec.Type == spec.SectionCover {
			continue // already drawn, or ignored in v1
		}
		var next *spec.Section
		if i+1 < len(r.sections) {
			next = &r.sections[i+1]
		}
		if err := r.renderSection(sec, next); err != nil {
			return err
		}
	}
	return nil
}

// fits reports whether a block of the given height still fits on the current
// page.
//
// maroto's FitlnCurrentPage counts the running header twice — once inside the
// rows already on the page, once as headerHeight — so it starts saying "no"
// one header-height early. That is the safe direction and it is deliberately
// not compensated for: erring early costs about 12mm of white space at the
// foot of a page, while erring late means maroto breaks the page itself,
// without re-emitting the table header the rows underneath belong to.
func (r *renderer) fits(height float64) bool {
	return r.m.FitlnCurrentPage(height)
}

// breakPage closes the current page. It is a no-op at the top of a fresh page,
// so a page_break section between two sections that already straddle a break
// does not leave a blank sheet in the middle of the document.
func (r *renderer) breakPage() { r.m.AddPages(page.New()) }

// ensure breaks the page when a block of the given height would not fit. The
// bool it does not return is deliberate: a block taller than a whole page
// still has to be drawn, and maroto will page it itself.
func (r *renderer) ensure(height float64) {
	if !r.fits(height) {
		r.breakPage()
	}
}

func (r *renderer) space(h float64) {
	r.m.AddRow(h, col.New(theme.GridCols))
}

// contentWidth is the usable width in millimetres — what a full-width column
// measures, and the denominator for every width computation in table.go.
func contentWidth() float64 { return theme.Page.ContentWidth() }

// colWidth converts grid units to millimetres.
func colWidth(units int) float64 {
	return contentWidth() * float64(units) / float64(theme.GridCols)
}

// brandMark draws the tenant logo when there is one and the Argentum wordmark
// when there is not. Both are the same call site so a document never has a
// hole where an identity should be.
func (r *renderer) brandMark(units int, height float64, size float64) core.Col {
	if len(r.opts.Brand.LogoPNG) > 0 {
		return col.New(units).Add(
			image.NewFromBytes(r.opts.Brand.LogoPNG, extension.Png, props.Rect{
				Percent: 100,
				Center:  false,
				Top:     1,
			}),
		)
	}
	name := strings.TrimSpace(r.opts.Brand.Name)
	if name == "" {
		name = "Argentum"
	}
	return col.New(units).Add(
		text.New(name, props.Text{
			Family: theme.FontDisplay,
			Style:  fontstyle.Bold,
			Size:   size,
			Color:  theme.ColorPrimary.Props(),
			Align:  align.Left,
			Top:    (height - lineHeight(size)) / 2,
		}),
	)
}

// headerRows is the running header: identity on the left, document title on
// the right, hairline underneath. It appears from page 2 and its height is
// exactly theme.Page.HeaderHeight, because that is the number every fit check
// in this file is computed against.
func (r *renderer) headerRows() []core.Row {
	const markUnits = 40
	titleHeight := theme.Page.HeaderHeight - 4

	title := r.title
	if title == "" {
		title = strings.TrimSpace(r.doc.Meta.Subject)
	}

	head := row.New(titleHeight).Add(
		r.brandMark(markUnits, titleHeight, theme.TypeScale.Caption+1),
		col.New(theme.GridCols-markUnits).Add(
			text.New(title, props.Text{
				Family: theme.FontBody,
				Size:   theme.TypeScale.Caption,
				Color:  theme.ColorMuted.Props(),
				Align:  align.Right,
				Top:    (titleHeight - lineHeight(theme.TypeScale.Caption)) / 2,
			}),
		),
	)
	rule := line.NewRow(4, props.Line{
		Color:       theme.ColorBorder.Props(),
		Thickness:   theme.Page.Hairline,
		SizePercent: 100,
	})
	return []core.Row{head, rule}
}

// footerRows is the running footer: a hairline, then the confidentiality label
// on the left and the generated-at stamp on the right. The page number is not
// here — maroto draws it below the footer band from the config's pattern,
// which is the only place "of {total}" can be filled in, because the total is
// not known until every page exists.
func (r *renderer) footerRows() []core.Row {
	bandHeight := theme.Page.FooterHeight - 3

	left := strings.TrimSpace(r.confid)
	if note := strings.TrimSpace(r.opts.Brand.FooterNote); note != "" {
		if left != "" {
			left += " · " + note
		} else {
			left = note
		}
	}
	right := r.labels.generated + " " + format.DateTime(r.genAt, r.fmt)

	caption := props.Text{
		Family: theme.FontBody,
		Size:   theme.TypeScale.Caption,
		Color:  theme.ColorMuted.Props(),
		Top:    1,
	}
	leftProps, rightProps := caption, caption
	leftProps.Align = align.Left
	rightProps.Align = align.Right

	rule := line.NewRow(3, props.Line{
		Color:       theme.ColorBorder.Props(),
		Thickness:   theme.Page.Hairline,
		SizePercent: 100,
	})
	band := row.New(bandHeight).Add(
		col.New(theme.GridCols/2).Add(text.New(left, leftProps)),
		col.New(theme.GridCols/2).Add(text.New(right, rightProps)),
	)
	return []core.Row{rule, band}
}
