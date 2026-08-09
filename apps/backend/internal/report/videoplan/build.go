package videoplan

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"math"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/flow"
	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/labels"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// BrandInput is the per-tenant identity as the caller holds it: pptx.Brand,
// field for field, so brand.Config fills one shape and all three renderers read
// it. Plan.Brand is what comes out the other side — the same identity resolved
// to hex and a data URI, because the renderer has no theme package and no
// network.
type BrandInput struct {
	Name    string
	LogoPNG []byte

	// Primary replaces the brand red. nil means the token. It is resolved
	// against both surfaces here — see Plan.Brand.PrimaryOnDark — because the
	// cover and the dividers are near-black and a tenant's accent is validated
	// against paper.
	Primary *theme.Color

	HideCredit      bool
	Confidentiality string
	FooterNote      string
}

// Limits bounds what a spec may turn into.
//
// They are checked before anything is built, and the reason is sharper here
// than it is for a document: a PDF that is too long wastes memory, and a video
// that is too long wastes minutes of a CPU that could be rendering somebody
// else's. The estimate that enforces them is a lower bound, so a spec that
// passes may still be refused after paging — but a spec that fails cannot
// possibly fit, and refusing it costs nothing.
type Limits struct {
	// MaxScenes bounds the scene count. Sixty scenes is at least three and a
	// half minutes even if every one of them is at the floor.
	MaxScenes int
	// MaxTotalFrames bounds the running time. 18 000 is ten minutes at 30fps.
	MaxTotalFrames int
	// MaxPlanBytes bounds the marshalled plan, which is dominated by chart
	// images and the logo.
	MaxPlanBytes int
}

// DefaultLimits is what an unset Limits resolves to.
var DefaultLimits = Limits{
	MaxScenes:      60,
	MaxTotalFrames: 18_000,
	MaxPlanBytes:   25 << 20,
}

// Normalize replaces non-positive fields with the defaults, matching how every
// other constructor in this tree treats bad input. A zero limit meaning
// "unlimited" would turn a forgotten config value into the exact failure these
// exist to prevent.
func (l Limits) Normalize() Limits {
	if l.MaxScenes <= 0 {
		l.MaxScenes = DefaultLimits.MaxScenes
	}
	if l.MaxTotalFrames <= 0 {
		l.MaxTotalFrames = DefaultLimits.MaxTotalFrames
	}
	if l.MaxPlanBytes <= 0 {
		l.MaxPlanBytes = DefaultLimits.MaxPlanBytes
	}
	return l
}

// Options is everything the projection needs that does not come from the model.
type Options struct {
	Brand BrandInput

	// Currency is the company's ISO 4217 default, used when the spec does not
	// name one.
	Currency string

	// Locale overrides the locale derived from the currency.
	Locale string

	// Now is the fallback generated-at when the spec omits one. Zero means
	// time.Now(); tests set it to keep plans stable.
	Now time.Time

	Limits Limits
}

// Build projects a document onto a plan.
//
// It is pure apart from the clock, and pure with it when the spec carries a
// generated_at: the same document produces the same plan, byte for byte, which
// is what makes a golden test possible and what makes "did this change the
// video" a question a diff can answer. The video itself is not byte-stable —
// see the package's locked decision 9 — so this is where determinism is proven
// instead.
func Build(doc *spec.Document, opts Options) (*Plan, error) {
	if doc == nil {
		return nil, fmt.Errorf("videoplan: nil spec")
	}
	b, err := newBuilder(doc, opts)
	if err != nil {
		return nil, err
	}
	if err := b.precheck(); err != nil {
		return nil, err
	}
	if err := flow.Walk(b.sections, doc.Cover(), b.title, b); err != nil {
		return nil, err
	}
	return b.finish()
}

type builder struct {
	doc  *spec.Document
	opts Options

	fmt    format.Options
	words  labels.Set
	title  string
	confid string
	genAt  time.Time
	limits Limits

	logoAspect float64

	sections []spec.Section
	scenes   []Scene
}

func newBuilder(doc *spec.Document, opts Options) (*builder, error) {
	b := &builder{doc: doc, opts: opts, limits: opts.Limits.Normalize()}

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
	b.fmt = format.Options{
		Locale:   loc,
		Currency: strings.ToUpper(strings.TrimSpace(currency)),
		Decimals: format.AutoDecimals,
	}
	b.words = labels.For(loc)

	b.title = strings.TrimSpace(doc.Title)
	b.sections = doc.Content.Sections

	// A spec with a bare content.table and no sections is the shape the model
	// uses for "just give me the data". It becomes a one-section document
	// rather than a special case, so the paging is the same code.
	if len(b.sections) == 0 && doc.Content.Table != nil {
		t := doc.Content.Table
		b.sections = []spec.Section{{
			Type:     spec.SectionTable,
			Columns:  t.Columns,
			Rows:     t.Rows,
			TotalRow: t.TotalRow,
			Caption:  t.Caption,
		}}
	}
	if len(b.sections) == 0 {
		return nil, fmt.Errorf("videoplan: content.sections or content.table required")
	}

	genAt, explicit := doc.Generated()
	if !explicit && !opts.Now.IsZero() {
		genAt = opts.Now
	}
	b.genAt = genAt

	b.confid = strings.TrimSpace(opts.Brand.Confidentiality)
	if c := doc.Cover(); c != nil && strings.TrimSpace(c.Confidentiality) != "" {
		b.confid = strings.TrimSpace(c.Confidentiality)
	}

	if len(opts.Brand.LogoPNG) > 0 {
		if cfg, err := png.DecodeConfig(bytes.NewReader(opts.Brand.LogoPNG)); err == nil && cfg.Height > 0 {
			b.logoAspect = float64(cfg.Width) / float64(cfg.Height)
		}
	}
	return b, nil
}

// precheck refuses a spec that cannot fit before a single chart is rasterised.
//
// The estimate is a **lower bound**: every content section becomes at least one
// scene of at least the floor duration, and prose is counted at its reading
// time. Paging only ever adds scenes, so a document this refuses could not have
// fitted. A document it accepts may still overrun, and finish() catches that —
// but by then the charts have been drawn, which is the cost this avoids paying
// for the specs that were never going to work.
func (b *builder) precheck() error {
	scenes, seconds := 0, 0.0
	if b.doc.Cover() != nil {
		scenes, seconds = scenes+1, seconds+CoverSeconds
	}
	for _, sec := range b.sections {
		switch sec.Type {
		case spec.SectionCover, spec.SectionPageBreak, spec.SectionSpacer:
			continue
		case spec.SectionHeading:
			if sec.Level != 2 {
				scenes, seconds = scenes+1, seconds+DividerSeconds
			}
		case spec.SectionParagraph:
			scenes, seconds = scenes+1, seconds+clamp(readSeconds(sec.Text))
		case spec.SectionCallout:
			scenes, seconds = scenes+1, seconds+clamp(readSeconds(sec.Title, sec.Text))
		default:
			scenes, seconds = scenes+1, seconds+MinSceneSeconds
		}
	}
	scenes, seconds = scenes+1, seconds+ClosingSeconds // the closing scene

	if scenes > b.limits.MaxScenes {
		return fmt.Errorf("videoplan: this document needs at least %d scenes and the limit is %d: "+
			"a video is a summary, not a transcript — reduce it to the sections that carry the argument",
			scenes, b.limits.MaxScenes)
	}
	if f := frames(seconds); f > b.limits.MaxTotalFrames {
		return fmt.Errorf("videoplan: this document runs to at least %s and the limit is %s: "+
			"a video is a summary, not a transcript — shorten the prose or split it into two reports",
			duration(f), duration(b.limits.MaxTotalFrames))
	}
	return nil
}

func (b *builder) finish() (*Plan, error) {
	if len(b.scenes) == 0 {
		return nil, fmt.Errorf("videoplan: nothing to render")
	}
	if len(b.scenes) > b.limits.MaxScenes {
		return nil, fmt.Errorf("videoplan: %d scenes, over the limit of %d",
			len(b.scenes), b.limits.MaxScenes)
	}

	total := 0
	for _, s := range b.scenes {
		total += s.Frames
	}
	if total > b.limits.MaxTotalFrames {
		return nil, fmt.Errorf("videoplan: %s of video, over the limit of %s",
			duration(total), duration(b.limits.MaxTotalFrames))
	}

	return &Plan{
		Version:     Version,
		Width:       1920,
		Height:      1080,
		FPS:         FPS,
		TotalFrames: total,
		Locale:      string(b.fmt.Locale),
		Title:       b.title,
		Metrics:     metrics(),
		Brand:       b.brand(),
		Scenes:      b.scenes,
	}, nil
}

// duration renders a frame count as m:ss, for a message a caller has to act on.
func duration(f int) string {
	secs := int(math.Round(float64(f) / FPS))
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func metrics() Metrics {
	return Metrics{
		MarginX:      canvas.Px(canvas.MarginX),
		MarginTop:    canvas.Px(canvas.MarginTop),
		MarginBottom: canvas.Px(canvas.MarginBottom),

		ContentWidth: canvas.Px(canvas.ContentWidth()),
		BodyTop:      canvas.Px(canvas.BodyTop()),
		BodyHeight:   canvas.Px(canvas.BodyHeight()),

		TitleBand:  canvas.Px(canvas.TitleBand),
		FooterBand: canvas.Px(canvas.FooterBand),
		FooterTop:  canvas.Px(canvas.FooterTop()),

		TitleRuleWidth:     canvas.Px(canvas.TitleRuleWidth),
		TitleRuleThickness: canvas.Px(canvas.TitleRuleThickness),

		Leading: canvas.BodyLeading,
		Type: TypeScale{
			Display: canvas.PtPx(canvas.Type.Display),
			H1:      canvas.PtPx(canvas.Type.H1),
			H2:      canvas.PtPx(canvas.Type.H2),
			Body:    canvas.PtPx(canvas.Type.Body),
			Caption: canvas.PtPx(canvas.Type.Caption),
		},
	}
}

// accent is the tenant's colour, or the brand red.
func (b *builder) accent() theme.Color {
	if c := b.opts.Brand.Primary; c != nil {
		return *c
	}
	return theme.ColorPrimary
}

func (b *builder) brand() Brand {
	credit := ""
	if !b.opts.Brand.HideCredit {
		credit = b.words.Credit
	}
	out := Brand{
		Name:    strings.TrimSpace(b.opts.Brand.Name),
		Primary: b.accent().Hex(),
		// The dark scenes get the accent lifted only as far as legibility
		// requires, so a colour that already works is untouched. Rejecting a
		// navy that is perfect on paper because the cover is near-black would
		// be fixing the wrong end — the deck's argument, unchanged.
		PrimaryOnDark:   theme.Readable(b.accent(), theme.ColorForeground, theme.White, theme.MinBrandContrast).Hex(),
		Foreground:      theme.ColorForeground.Hex(),
		Background:      theme.ColorBackground.Hex(),
		Muted:           theme.ColorMuted.Hex(),
		Border:          theme.ColorBorder.Hex(),
		Dark:            theme.ColorForeground.Hex(),
		OnDark:          theme.ColorBackground.Hex(),
		Credit:          credit,
		Confidentiality: b.confid,
		FooterNote:      strings.TrimSpace(b.opts.Brand.FooterNote),
	}
	if b.logoAspect > 0 {
		out.LogoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.opts.Brand.LogoPNG)
		out.LogoAspect = math.Round(b.logoAspect*1000) / 1000
	}
	return out
}
