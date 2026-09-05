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

// PromoImage is a picture the tenant uploaded, resolved by the caller and
// handed to the projection ready to draw (T-G12).
//
// The bytes travel rather than a key or a URL, for the reason every image in
// a plan does: the render service has no network and no credentials. Resolving
// which image is *whose* happens far from here — in the tool, against the
// company's own library — so this package never sees a tenant boundary it
// could get wrong.
type PromoImage struct {
	PNG []byte
	// Aspect is width over height. Zero is treated as square.
	Aspect float64
	// Alt describes the picture for the slide's alt text.
	Alt string
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

	// MinSlides and MaxSlides bound a carousel (T-G3). The ceiling is
	// Instagram's — a carousel is at most ten children — and a spec outside
	// the band is refused rather than truncated, because which slide to drop
	// is the model's decision about its own argument, not this package's. A
	// video ignores them; a carousel ignores MaxScenes and MaxTotalFrames.
	//
	// **The floor is 1 since T-G11, and it was 2 before.** A promotion, a
	// launch or a single figure is one image, and a floor of two made the
	// commonest marketing post the one shape this pipeline refused — it would
	// answer "add a section" to a spec that was already exactly what the user
	// asked for. Nothing downstream ever needed two: the pages, the manifest,
	// the zip and the announcement are all written per page.
	MinSlides int
	MaxSlides int
}

// DefaultLimits is what an unset Limits resolves to.
var DefaultLimits = Limits{
	MaxScenes:      60,
	MaxTotalFrames: 18_000,
	MaxPlanBytes:   25 << 20,
	MinSlides:      1,
	MaxSlides:      10,
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
	if l.MinSlides <= 0 {
		l.MinSlides = DefaultLimits.MinSlides
	}
	if l.MaxSlides <= 0 {
		l.MaxSlides = DefaultLimits.MaxSlides
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

	// Images are the tenant's uploaded pictures, by image id, for the promo
	// cards in this spec (T-G12). Empty is ordinary: every spec without a
	// promo section, and a promo whose image could not be resolved, which
	// draws the card without a photograph rather than failing.
	Images map[string]PromoImage

	// Now is the fallback generated-at when the spec omits one. Zero means
	// time.Now(); tests set it to keep plans stable.
	Now time.Time

	// Surface is the geometry every line is measured against and every scene
	// is positioned on. The zero value is canvas.Wide — the 1920×1080 frame —
	// so a caller that predates surfaces (T-G2) builds the plan it always did.
	// T-G3 hands canvas.Portrait here for a carousel.
	Surface canvas.Surface

	Limits Limits
}

// CheckLimits answers "could this spec ever be a video?" without drawing one.
//
// It runs Build's own precheck and stops there, so the answer is the same
// sentence a render would have failed with — one estimate, not a second
// implementation of the caps able to disagree with the first.
//
// This exists because the caps have to be applied twice, in two processes. The
// worker applies them inside Build, where the plan is actually assembled; the
// `/v1` door applies them here, before it queues anything. The 2026-08-09 gate
// found the door skipping them: a 242-section spec was accepted with `202
// queued` and only refused a minute later by the worker, so a caller who could
// have been told at the door had to write a collection path to be told that
// their document can never render. `spec.CheckLimits` had always been called
// there — it bounds rows, columns and chart points, and knows nothing about
// scenes or running time.
//
// No chart is rasterised and no logo is needed: precheck reads section kinds
// and prose lengths only.
func CheckLimits(doc *spec.Document, opts Options) error {
	if doc == nil {
		return fmt.Errorf("videoplan: nil spec")
	}
	b, err := newBuilder(doc, opts)
	if err != nil {
		return err
	}
	return b.precheck()
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
	return project(doc, opts, false)
}

// BuildCarousel projects a document onto a carousel: a still plan on the
// portrait surface, one frame a scene, two to ten scenes, every scene carrying
// its alt text (T-G3).
//
// It is Build with the time axis removed. The walk, the chart images, the logo,
// the line breaking and the table solver are the video's, against
// canvas.Portrait instead of canvas.Wide; what changes is that no scene has a
// duration — timing.go is never consulted — and that the count is bounded by
// Limits.MinSlides/MaxSlides rather than by scenes and running time. A caller
// that sets Options.Surface chooses a different still surface (T-G9's square
// and story); the zero value is Portrait here where it is Wide for a video.
func BuildCarousel(doc *spec.Document, opts Options) (*Plan, error) {
	opts.Surface = opts.Surface.Or(SurfaceFor(doc))
	return project(doc, opts, true)
}

// SurfaceFor is the frame a spec's slides are drawn on: the size named in its
// social block, or Portrait when it names none (T-G11).
//
// An unknown name resolves to Portrait rather than failing, and the refusal
// that matters happens earlier — `spec.Validate` rejects a size that is not
// one of the four, in the turn, where the model can still fix it. By the time
// a plan is being built the spec has been validated, so this function's job
// is to be total rather than to be a second gate with its own opinion.
func SurfaceFor(doc *spec.Document) canvas.Surface {
	if doc == nil || doc.Social == nil {
		return canvas.Portrait
	}
	switch doc.Social.Size {
	case spec.SizeSquare:
		return canvas.Square
	case spec.SizeStory:
		return canvas.Story
	case spec.SizeLandscape:
		// The video's own frame, held still. A 16:9 surface for a feed post is
		// the one size here that was already drawn, measured and golden-tested
		// before this ticket, so it costs a case rather than a surface.
		return canvas.Wide
	default:
		return canvas.Portrait
	}
}

// CheckCarouselLimits is CheckLimits for a carousel: the same precheck
// BuildCarousel runs, stopped before anything is drawn, so the `/v1` door and
// the tool can refuse an eleven-slide spec in milliseconds with the sentence
// the worker would have used a minute later.
func CheckCarouselLimits(doc *spec.Document, opts Options) error {
	if doc == nil {
		return fmt.Errorf("videoplan: nil spec")
	}
	opts.Surface = opts.Surface.Or(SurfaceFor(doc))
	b, err := newBuilder(doc, opts)
	if err != nil {
		return err
	}
	b.still = true
	return b.precheck()
}

// project is Build and BuildCarousel's shared body.
func project(doc *spec.Document, opts Options, still bool) (*Plan, error) {
	if doc == nil {
		return nil, fmt.Errorf("videoplan: nil spec")
	}
	b, err := newBuilder(doc, opts)
	if err != nil {
		return nil, err
	}
	b.still = still
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

	// still is a carousel: every scene is one frame and the slide band, not
	// the running time, is the limit. Read in precheck and finish only — the
	// sinks compute frames as they always did and finish overwrites them,
	// which keeps the eight sink methods identical for both outputs.
	still bool

	fmt    format.Options
	words  labels.Set
	title  string
	confid string
	genAt  time.Time
	limits Limits

	logoAspect float64

	// s is the surface, resolved once from opts so no scene can be measured
	// against a different one from its neighbours.
	s canvas.Surface

	sections []spec.Section
	scenes   []Scene
}

func newBuilder(doc *spec.Document, opts Options) (*builder, error) {
	b := &builder{doc: doc, opts: opts, limits: opts.Limits.Normalize(), s: opts.Surface.Or(canvas.Wide)}

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
	scenes, seconds := b.estimate()
	if b.still {
		return b.checkSlides(scenes, true)
	}

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

// checkSlides refuses a carousel outside the slide band with the sentence the
// model can act on. lowerBound says the count is precheck's estimate rather
// than the finished plan's: a table that pages adds slides, so an estimate
// over the ceiling is certainly over it, while an estimate under the floor is
// exact — nothing counted under the floor can page into more.
func (b *builder) checkSlides(n int, lowerBound bool) error {
	switch {
	case n > b.limits.MaxSlides:
		qualifier := ""
		if lowerBound {
			qualifier = "at least "
		}
		return fmt.Errorf("videoplan: a carousel is %d–%d slides; this spec makes %s%d — merge or drop sections",
			b.limits.MinSlides, b.limits.MaxSlides, qualifier, n)
	case n < b.limits.MinSlides:
		return fmt.Errorf("videoplan: a carousel is %d–%d slides; this spec makes %d — add a section",
			b.limits.MinSlides, b.limits.MaxSlides, n)
	}
	return nil
}

// estimate is the lower bound on scenes and seconds precheck reasons from.
func (b *builder) estimate() (scenes int, seconds float64) {
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
		case spec.SectionCallout, spec.SectionHero, spec.SectionPromo:
			scenes, seconds = scenes+1, seconds+clamp(readSeconds(sec.Title, sec.Text))
		default:
			scenes, seconds = scenes+1, seconds+MinSceneSeconds
		}
	}
	scenes, seconds = scenes+1, seconds+ClosingSeconds // the closing scene
	return scenes, seconds
}

func (b *builder) finish() (*Plan, error) {
	if len(b.scenes) == 0 {
		return nil, fmt.Errorf("videoplan: nothing to render")
	}
	if b.still {
		return b.finishStill()
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
		Width:       b.s.PxW,
		Height:      b.s.PxH,
		FPS:         FPS,
		TotalFrames: total,
		Locale:      string(b.fmt.Locale),
		Title:       b.title,
		Metrics:     metrics(b.s),
		Brand:       b.brand(),
		Scenes:      b.scenes,
	}, nil
}

// finishStill is finish for a carousel: the exact slide count against the
// band, one frame a scene, fps 1, and the alt text every still needs.
func (b *builder) finishStill() (*Plan, error) {
	b.trimClosing()
	if len(b.scenes) == 0 {
		return nil, fmt.Errorf("videoplan: this spec makes no slide with anything on it — " +
			"every section in it is empty")
	}
	if err := b.checkSlides(len(b.scenes), false); err != nil {
		return nil, err
	}
	scenes := make([]Scene, len(b.scenes))
	for i, s := range b.scenes {
		s.Frames = 1
		s.Alt = altText(s, b.words)
		scenes[i] = s
	}
	return &Plan{
		Version:     Version,
		Width:       b.s.PxW,
		Height:      b.s.PxH,
		FPS:         1,
		TotalFrames: len(scenes),
		Still:       true,
		Locale:      string(b.fmt.Locale),
		Title:       b.title,
		Metrics:     metrics(b.s),
		Brand:       b.brand(),
		Scenes:      scenes,
	}, nil
}

// trimClosing drops the sign-off card from a carousel that would otherwise be
// a single image, and from one that is nothing else (T-G11).
//
// The closing scene is a report's outro: the brand and the generated-at
// timestamp on the dark ground. On a seven-slide carousel that is a
// conventional last card and it stays. On a **one-image post** — a
// promotion, a launch, one figure — it is half the post, and it made the
// commonest marketing request impossible to satisfy: the spec said one image
// and the pipeline produced two, the second of them a timestamp.
//
// A plan whose only scene is the closing card is not a post at all. It is
// left empty here and refused by the caller, because "your carousel is ready"
// followed by a single card saying when it was made is worse than the
// refusal that names what was wrong with the spec.
func (b *builder) trimClosing() {
	switch {
	case len(b.scenes) == 1 && b.scenes[0].Kind == KindClosing:
		b.scenes = nil
	case len(b.scenes) == 2 && b.scenes[1].Kind == KindClosing:
		b.scenes = b.scenes[:1]
	}
}

// tones is the callout palette, resolved once per plan.
//
// The mapping is the PDF's and the deck's, which both carry their own copy of
// it — a third copy would be one too many, so this is the one that travels and
// the renderer holds none. The tones deliberately do not all resolve to the
// brand red: a warning and a good-news box that are the same colour communicate
// nothing.
//
// The fill is the accent at 8%, which is how both document renderers tint a
// callout ground: light enough that body text keeps its contrast against it,
// dark enough to read as a box rather than as a stray rule.
func tones() map[string]Tone {
	out := map[string]Tone{}
	for name, c := range map[string]theme.Color{
		spec.ToneInfo: theme.ColorInfo,
		spec.ToneWarn: theme.ColorWarning,
		spec.ToneGood: theme.ColorPositive,
	} {
		out[name] = Tone{
			Accent: c.Hex(),
			Fill:   c.Tint(calloutFillTint).Hex(),
		}
	}
	return out
}

// calloutFillTint is how far a tone is lifted towards the page for its ground.
const calloutFillTint = 0.92

// duration renders a frame count as m:ss, for a message a caller has to act on.
func duration(f int) string {
	secs := int(math.Round(float64(f) / FPS))
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// metrics is the surface in CSS pixels, exactly as the scenes were measured
// against it.
func metrics(s canvas.Surface) Metrics {
	return Metrics{
		MarginX:      canvas.Px(s.MarginX),
		MarginTop:    canvas.Px(s.MarginTop),
		MarginBottom: canvas.Px(s.MarginBottom),

		ContentWidth: canvas.Px(s.ContentWidth()),
		BodyTop:      canvas.Px(s.BodyTop()),
		BodyHeight:   canvas.Px(s.BodyHeight()),

		TitleBand:  canvas.Px(s.TitleBand),
		FooterBand: canvas.Px(s.FooterBand),
		FooterTop:  canvas.Px(s.FooterTop()),

		TitleRuleWidth:     canvas.Px(s.TitleRuleWidth),
		TitleRuleThickness: canvas.Px(s.TitleRuleThickness),

		Radius:    canvas.Px(theme.RadiusBase),
		SpacingSM: canvas.Px(theme.Spacing.SM),
		SpacingMD: canvas.Px(theme.Spacing.MD),
		SpacingLG: canvas.Px(theme.Spacing.LG),

		Leading: canvas.BodyLeading,
		Type: TypeScale{
			Display: canvas.PtPx(s.Type.Display),
			H1:      canvas.PtPx(s.Type.H1),
			H2:      canvas.PtPx(s.Type.H2),
			Body:    canvas.PtPx(s.Type.Body),
			Caption: canvas.PtPx(s.Type.Caption),
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

// hasPromo reports whether any scene is a promotion card.
func (b *builder) hasPromo() bool {
	for _, s := range b.scenes {
		if s.Kind == KindPromo {
			return true
		}
	}
	return false
}

// promoBrand derives the promotion card's five colours from the tenant's
// accent (T-G12).
//
// Every one is a function of the same colour, so a shop with a green brand
// gets a green promotion rather than Argentum's red wearing their logo. The
// ratios are the argument:
//
//   - The sunburst's two wedges differ by 12% towards white. Enough to read as
//     rays under a photograph, not enough to compete with one — the reference
//     posters this was drawn against all use a low-contrast ground for
//     exactly that reason.
//   - The star behind the product is 14% towards black, so the product's
//     edges have something to sit against whatever colour the accent is.
//   - The badge and the price panel are the same colour, 28% towards black:
//     the two loudest things on the card should not be two different reds,
//     and a shopper's eye is meant to travel from one to the other.
func promoBrand(accent theme.Color) *PromoBrand {
	strong := accent.Mix(theme.ColorForeground, 0.28)
	return &PromoBrand{
		Ground:     accent.Hex(),
		Ray:        accent.Tint(0.12).Hex(),
		Burst:      accent.Mix(theme.ColorForeground, 0.14).Hex(),
		Badge:      strong.Hex(),
		PriceBlock: strong.Hex(),
	}
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
		Surface:         theme.ColorSurface.Hex(),
		SurfaceSubtle:   theme.ColorSurfaceSubtle.Hex(),
		Positive:        theme.ColorPositive.Hex(),
		Destructive:     theme.ColorDestructive.Hex(),
		Tones:           tones(),
		Credit:          credit,
		Confidentiality: b.confid,
		FooterNote:      strings.TrimSpace(b.opts.Brand.FooterNote),
	}
	// The promotion palette rides only on the plans that need it, so a report,
	// a deck and every carousel built before T-G12 carry the byte-identical
	// brand block they did before.
	if b.hasPromo() {
		out.Promo = promoBrand(b.accent())
	}
	if b.logoAspect > 0 {
		out.LogoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.opts.Brand.LogoPNG)
		out.LogoAspect = math.Round(b.logoAspect*1000) / 1000
	}
	return out
}
