package videoplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The carousel fixture is this package's own rather than one of the PDF's,
// which is a deliberate exception to the rule at the top of build_test.go.
// Every PDF fixture is either too long for a carousel — monthly_sales makes
// thirteen beats, and Instagram takes ten — or has no chart and no KPI row,
// which are the two things a carousel exists to show. So the fixture is the
// spec a Marketing agent would actually write for one: a cover, three cards, a
// chart, a short table and a callout. It is still the PDF's schema, and it
// still renders as a PDF; nothing in it is carousel-specific.
const carouselFixture = "carousel.json"

func loadCarouselFixture(t *testing.T) *spec.Document {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", carouselFixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc spec.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", carouselFixture, err)
	}
	doc.Normalize()
	return &doc
}

func buildCarousel(t *testing.T) *Plan {
	t.Helper()
	p, err := BuildCarousel(loadCarouselFixture(t), Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	return p
}

// TestGoldenCarouselPlan is the sixth golden, on the portrait surface. Same
// digesting, same -update flag as the five wide ones.
func TestGoldenCarouselPlan(t *testing.T) {
	got := mustJSON(t, digestImages(buildCarousel(t)))
	golden := filepath.Join("testdata", "carousel.plan.json")

	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(got))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/report/videoplan -update`): %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
		t.Errorf("plan differs from %s; run with -update to see the change as a diff", golden)
	}
}

// TestACarouselIsAStillPlanOnThePortraitSurface pins decision 4: fps 1, one
// frame a scene, the still flag, and the frame the research asked for.
func TestACarouselIsAStillPlanOnThePortraitSurface(t *testing.T) {
	p := buildCarousel(t)

	if !p.Still {
		t.Error("plan is not marked still")
	}
	if p.FPS != 1 {
		t.Errorf("fps = %d, want 1", p.FPS)
	}
	if p.Width != 1080 || p.Height != 1350 {
		t.Errorf("frame is %d×%d, want 1080×1350", p.Width, p.Height)
	}
	if p.TotalFrames != len(p.Scenes) {
		t.Errorf("total_frames = %d over %d scenes; a still plan has one frame a scene", p.TotalFrames, len(p.Scenes))
	}
	for i, s := range p.Scenes {
		if s.Frames != 1 {
			t.Errorf("scene %d (%s) has %d frames, want 1", i, s.Kind, s.Frames)
		}
	}
	if n := len(p.Scenes); n < DefaultLimits.MinSlides || n > DefaultLimits.MaxSlides {
		t.Errorf("%d slides, outside %d–%d", n, DefaultLimits.MinSlides, DefaultLimits.MaxSlides)
	}

	// The same 2 px/pt as the wide surface: the portrait H1 is the wide H1.
	if p.Metrics.Type.H1 != 58 || p.Metrics.Type.Body != 36 {
		t.Errorf("H1 %dpx / body %dpx, want 58 / 36 — the portrait surface changed the type", p.Metrics.Type.H1, p.Metrics.Type.Body)
	}
	// Instagram's chrome covers ~120 px at the top and ~150 px at the bottom.
	if p.Metrics.MarginTop < 120 {
		t.Errorf("margin_top %dpx is inside the top safe zone", p.Metrics.MarginTop)
	}
	if p.Metrics.MarginBottom < 150 {
		t.Errorf("margin_bottom %dpx is inside the bottom safe zone", p.Metrics.MarginBottom)
	}
	if p.Metrics.ContentWidth != canvas.Px(canvas.Portrait.ContentWidth()) {
		t.Errorf("content_width %dpx is not the portrait measure", p.Metrics.ContentWidth)
	}
}

// TestEverySlideCarriesAltText: non-empty, within the platform cap, and made
// of the slide's own strings — a KPI slide's alt names its cards.
func TestEverySlideCarriesAltText(t *testing.T) {
	p := buildCarousel(t)
	for i, s := range p.Scenes {
		if strings.TrimSpace(s.Alt) == "" {
			t.Errorf("scene %d (%s) has no alt text", i, s.Kind)
		}
		if n := utf8.RuneCountInString(s.Alt); n > MaxAltChars {
			t.Errorf("scene %d (%s) alt is %d runes, over %d", i, s.Kind, n, MaxAltChars)
		}
		switch s.Kind {
		case KindKPI:
			for _, k := range s.KPIs {
				if !strings.Contains(s.Alt, k.Label) || !strings.Contains(s.Alt, k.Value) {
					t.Errorf("KPI slide alt %q does not name the card %q = %q", s.Alt, k.Label, k.Value)
				}
			}
		case KindChart, KindTable:
			if len(s.Caption) > 0 && !strings.Contains(s.Alt, s.Caption[0]) {
				t.Errorf("%s slide alt %q does not carry its caption", s.Kind, s.Alt)
			}
		}
		// Indonesian fixture, Indonesian alt: the values went through the
		// same formatter as the slide, so the magnitude word is the locale's.
		if s.Kind == KindKPI && !strings.Contains(s.Alt, "Juta") {
			t.Errorf("KPI alt %q carries no Indonesian magnitude word", s.Alt)
		}
	}
}

// TestAltTextIsCappedAtTheCarouselLimit exercises the cap directly: a value
// longer than the platform allows is cut at a word with an ellipsis, never
// mid-rune.
func TestAltTextIsCappedAtTheCarouselLimit(t *testing.T) {
	long := strings.Repeat("Pendapatan naik ", 100) // 1600 runes
	got := capRunes(long, MaxAltChars)
	if n := utf8.RuneCountInString(got); n > MaxAltChars {
		t.Errorf("capped alt is %d runes", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("capped alt does not end in an ellipsis: %q", got[len(got)-20:])
	}
	if !utf8.ValidString(got) {
		t.Error("capped alt is not valid UTF-8")
	}
	if capRunes("short", MaxAltChars) != "short" {
		t.Error("a short string was altered")
	}
}

// TestAVideoPlanIsNotStill: the flag and the alt text are the carousel's, and
// the five wide goldens prove the rest of the video path is untouched.
func TestAVideoPlanIsNotStill(t *testing.T) {
	p, err := Build(loadCarouselFixture(t), Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Still {
		t.Error("a video plan reports still")
	}
	if p.FPS != FPS || p.Width != 1920 {
		t.Errorf("video plan is %d fps at %d wide", p.FPS, p.Width)
	}
	for i, s := range p.Scenes {
		if s.Alt != "" {
			t.Errorf("video scene %d carries alt text %q", i, s.Alt)
		}
	}
}

// TestTooManySlidesAreRefusedBeforeAnythingIsBuilt: the monthly report the
// PDF, the deck and the video all render makes thirteen beats, and a carousel
// is ten. It is refused at the door — CheckCarouselLimits, before a chart is
// drawn — with the sentence the model can act on, and BuildCarousel says the
// same thing.
func TestTooManySlidesAreRefusedBeforeAnythingIsBuilt(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")

	err := CheckCarouselLimits(doc, Options{Now: fixtureNow})
	if err == nil {
		t.Fatal("a thirteen-beat report was accepted as a carousel")
	}
	for _, want := range []string{"a carousel is 1–10 slides", "at least 13", "merge or drop sections"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}
	if _, err := BuildCarousel(doc, Options{Now: fixtureNow}); err == nil || !strings.Contains(err.Error(), "a carousel is 1–10 slides") {
		t.Errorf("BuildCarousel: %v, want the slide-band refusal", err)
	}
}

// TestOneSlideIsAllowed: the floor is 1 since T-G11, and the reason is the
// commonest marketing post there is — a promotion or a launch is one image.
// A floor of 2 answered "add a section" to a spec that was already what the
// user asked for.
//
// The spec here is one hero and nothing else: the single image a discount
// post is. It produces exactly one page, so nothing downstream has to treat
// it specially — the pages, the manifest and the announcement are per page.
func TestOneSlideIsAllowed(t *testing.T) {
	doc := &spec.Document{
		Title:  "Diskon akhir pekan",
		Social: &spec.Social{Caption: "Diskon 20% sampai Minggu."},
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHero, Subtitle: "PROMO AKHIR PEKAN", Title: "Diskon 20%",
				Text: "Semua kopi susu, Jumat sampai Minggu."},
		}},
	}
	doc.Normalize()

	if err := CheckCarouselLimits(doc, Options{Now: fixtureNow}); err != nil {
		t.Fatalf("the door refused a one-slide spec: %v", err)
	}
	p, err := BuildCarousel(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	if len(p.Scenes) != 1 {
		t.Fatalf("a lone hero made %d slides, want 1", len(p.Scenes))
	}
	if p.Scenes[0].Kind != KindHero {
		t.Errorf("slide 1 is %q, want %q", p.Scenes[0].Kind, KindHero)
	}
	if p.TotalFrames != 1 {
		t.Errorf("total_frames = %d, want 1", p.TotalFrames)
	}
	// The alt text is what a screen reader and a publisher read, and a hero's
	// is the only description of a slide that is otherwise all type.
	for _, want := range []string{"Diskon 20%", "PROMO AKHIR PEKAN", "Semua kopi susu"} {
		if !strings.Contains(p.Scenes[0].Alt, want) {
			t.Errorf("alt %q omits %q", p.Scenes[0].Alt, want)
		}
	}
}

// A spec that makes no slide at all is still refused: the floor moved to one,
// not to zero.
func TestNoSlidesIsStillRefused(t *testing.T) {
	doc := &spec.Document{
		Title: "Nothing here",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHero},
		}},
	}
	doc.Normalize()

	if _, err := BuildCarousel(doc, Options{Now: fixtureNow}); err == nil {
		t.Error("a spec with nothing to draw became a carousel")
	}
}

// TestATableThatPagesPastTheBandIsRefusedAtFinish: the precheck counts a
// table once, so a long one passes the door and is refused, exactly, once the
// surface has paged it. Both refusals carry the same sentence.
func TestATableThatPagesPastTheBandIsRefusedAtFinish(t *testing.T) {
	doc := loadFixture(t, "export_200.json") // cover, one paragraph, a 200-row table, a footnote

	if err := CheckCarouselLimits(doc, Options{Now: fixtureNow}); err != nil {
		t.Fatalf("the door refused a spec whose lower bound is inside the band: %v", err)
	}
	_, err := BuildCarousel(doc, Options{Now: fixtureNow})
	if err == nil {
		t.Fatal("a 200-row table became a carousel")
	}
	if !strings.Contains(err.Error(), "a carousel is 1–10 slides; this spec makes ") || strings.Contains(err.Error(), "at least") {
		t.Errorf("finish refusal %q should state the exact count", err)
	}
}

// TestTheSlideBandIsConfigurable: Limits.MinSlides/MaxSlides are read, and a
// zero is the default rather than "unlimited".
func TestTheSlideBandIsConfigurable(t *testing.T) {
	doc := loadCarouselFixture(t)
	if _, err := BuildCarousel(doc, Options{Now: fixtureNow, Limits: Limits{MaxSlides: 3}}); err == nil {
		t.Error("MaxSlides 3 accepted a seven-slide carousel")
	}
	if _, err := BuildCarousel(doc, Options{Now: fixtureNow, Limits: Limits{MinSlides: 20}}); err == nil {
		t.Error("MinSlides 20 accepted a seven-slide carousel")
	}
	if l := (Limits{}).Normalize(); l.MinSlides != 1 || l.MaxSlides != 10 {
		t.Errorf("zero limits normalise to %d–%d, want 1–10", l.MinSlides, l.MaxSlides)
	}
}

// TestPortraitWrapsNarrowerThanWide: the same fixture on the two surfaces
// breaks the same cover title into more lines on the narrower one, which is the
// whole reason the surface travels with the plan.
func TestPortraitWrapsNarrowerThanWide(t *testing.T) {
	doc := loadCarouselFixture(t)
	wide, err := Build(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	portrait := buildCarousel(t)
	if portrait.Metrics.ContentWidth >= wide.Metrics.ContentWidth {
		t.Fatalf("portrait measure %dpx is not narrower than wide %dpx", portrait.Metrics.ContentWidth, wide.Metrics.ContentWidth)
	}
	if len(portrait.Scenes[0].Title) < len(wide.Scenes[0].Title) {
		t.Errorf("cover title is %d lines portrait, %d wide — the narrower surface wrapped less", len(portrait.Scenes[0].Title), len(wide.Scenes[0].Title))
	}
}

// TestWriteCarouselPlan is TestWritePlans for the carousel: the undigested,
// renderable plan for apps/render's fixture CLI, behind the same variable.
//
//	ARGENTUM_PLAN_OUT=/tmp/plans go test ./internal/report/videoplan
//	pnpm --filter @argentum/render render:fixture /tmp/plans/carousel.plan.json out --stills
func TestWriteCarouselPlan(t *testing.T) {
	dir := os.Getenv("ARGENTUM_PLAN_OUT")
	if dir == "" {
		t.Skip("set ARGENTUM_PLAN_OUT to write the carousel plan to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out := mustJSON(t, buildCarousel(t))
	path := filepath.Join(dir, "carousel.plan.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(out))
}

// The size named in the spec chooses the frame the slides are drawn on
// (T-G11), and the plan carries that frame — which is what makes the line
// breaks in it right, because they were measured against it.
func TestTheSocialSizeChoosesTheSurface(t *testing.T) {
	cases := []struct {
		size string
		w, h int
	}{
		{"", 1080, 1350}, // omitted is portrait, which is what every carousel before T-G11 was
		{spec.SizePortrait, 1080, 1350},
		{spec.SizeSquare, 1080, 1080},
		{spec.SizeStory, 1080, 1920},
		{spec.SizeLandscape, 1920, 1080},
	}
	for _, tc := range cases {
		t.Run(firstNonEmptyName(tc.size), func(t *testing.T) {
			doc := loadCarouselFixture(t)
			if doc.Social == nil {
				doc.Social = &spec.Social{}
			}
			doc.Social.Size = tc.size
			p, err := BuildCarousel(doc, Options{Now: fixtureNow})
			if err != nil {
				t.Fatalf("BuildCarousel: %v", err)
			}
			if p.Width != tc.w || p.Height != tc.h {
				t.Errorf("plan is %dx%d, want %dx%d", p.Width, p.Height, tc.w, tc.h)
			}
			if p.Metrics.MarginX == 0 {
				t.Error("the plan carries no metrics for its surface")
			}
		})
	}
}

// The door and the worker must agree about the frame, because the door's job
// is to refuse in the turn what the worker would refuse minutes later. A
// surface with a shorter body pages a table into more slides, so a size that
// the precheck ignored could pass the door and fail the render.
func TestTheDoorChecksTheSizeItWillBeBuiltOn(t *testing.T) {
	doc := loadCarouselFixture(t)
	if doc.Social == nil {
		doc.Social = &spec.Social{}
	}
	doc.Social.Size = spec.SizeSquare

	doorErr := CheckCarouselLimits(doc, Options{Now: fixtureNow, Limits: Limits{MaxSlides: 3}})
	_, buildErr := BuildCarousel(doc, Options{Now: fixtureNow, Limits: Limits{MaxSlides: 3}})
	if doorErr == nil || buildErr == nil {
		t.Fatalf("door = %v, build = %v; both must refuse", doorErr, buildErr)
	}
	// And the surface actually reached the builder: a square body is 106mm
	// against portrait's 154, so the same fixture makes at least as many
	// slides on it.
	square, err := BuildCarousel(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("square: %v", err)
	}
	doc.Social.Size = spec.SizePortrait
	portrait, err := BuildCarousel(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("portrait: %v", err)
	}
	if len(square.Scenes) < len(portrait.Scenes) {
		t.Errorf("square made %d slides and portrait %d; the shorter body cannot make fewer",
			len(square.Scenes), len(portrait.Scenes))
	}
}

// A hero is one statement on the frame: kicker, headline, supporting line,
// and none of a report's furniture (T-G11).
func TestAHeroIsItsOwnBeatWithNoTitle(t *testing.T) {
	doc := &spec.Document{
		Title: "Promo",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHeading, Level: 1, Text: "Penawaran"},
			{Type: spec.SectionHero, Subtitle: "PROMO AKHIR PEKAN", Title: "Diskon 20%",
				Text: "Semua kopi susu, Jumat sampai Minggu."},
			{Type: spec.SectionParagraph, Text: "Berlaku di semua cabang."},
		}},
	}
	doc.Normalize()

	p, err := BuildCarousel(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	var hero *Scene
	for i := range p.Scenes {
		if p.Scenes[i].Kind == KindHero {
			hero = &p.Scenes[i]
		}
	}
	if hero == nil {
		t.Fatalf("no hero scene in %d slides", len(p.Scenes))
	}
	// The headline is the title, and the heading above it does not travel:
	// a hero under a section heading would put two voices on one frame.
	if strings.Join(hero.Title, " ") != "Diskon 20%" {
		t.Errorf("headline = %q, want the hero's own title", hero.Title)
	}
	if strings.Join(hero.Subtitle, " ") != "PROMO AKHIR PEKAN" {
		t.Errorf("kicker = %q", hero.Subtitle)
	}
	if len(hero.Lines) == 0 || !strings.Contains(strings.Join(hero.Lines, " "), "kopi susu") {
		t.Errorf("supporting line = %q", hero.Lines)
	}
	// A hero carries no table, chart, KPI or facts: it is type on a ground.
	if hero.Table != nil || hero.Chart != nil || len(hero.KPIs) > 0 || len(hero.Facts) > 0 {
		t.Error("a hero carries report furniture")
	}
	// The heading before it still made its own divider, and the paragraph
	// after it still made its own statement: a hero interrupts nothing.
	if len(p.Scenes) < 4 {
		t.Errorf("%d slides; the heading, hero, paragraph and closing should all be there", len(p.Scenes))
	}
}

// One field is a headline, not a headline repeated as its own supporting line.
func TestAHeroWithOneFieldSaysItOnce(t *testing.T) {
	doc := &spec.Document{
		Title: "Promo",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHero, Text: "Diskon 20% akhir pekan ini"},
			{Type: spec.SectionParagraph, Text: "Berlaku di semua cabang."},
		}},
	}
	doc.Normalize()
	p, err := BuildCarousel(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	h := p.Scenes[0]
	if h.Kind != KindHero {
		t.Fatalf("slide 1 is %q", h.Kind)
	}
	if strings.Join(h.Title, " ") != "Diskon 20% akhir pekan ini" {
		t.Errorf("headline = %q", h.Title)
	}
	if len(h.Lines) != 0 {
		t.Errorf("the same sentence was drawn twice: %q", h.Lines)
	}
}

// The video draws a hero too — it is a section type, not a carousel feature —
// and it gets a duration like every other scene rather than a zero-length beat.
func TestAHeroInAVideoHasADuration(t *testing.T) {
	doc := &spec.Document{
		Title: "Promo",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHero, Title: "Diskon 20%", Text: "Semua kopi susu."},
			{Type: spec.SectionParagraph, Text: "Berlaku di semua cabang bulan ini, termasuk gerai baru."},
		}},
	}
	doc.Normalize()
	p, err := Build(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Scenes[0].Kind != KindHero || p.Scenes[0].Frames < 1 {
		t.Errorf("scene 1 = %q at %d frames", p.Scenes[0].Kind, p.Scenes[0].Frames)
	}
}

func firstNonEmptyName(s string) string {
	if s == "" {
		return "omitted"
	}
	return s
}

// A promotion card is a whole post: one section, one slide, both prices
// formatted, and the photograph inlined (T-G12).
func TestAPromoCardIsAWholePost(t *testing.T) {
	doc := &spec.Document{
		Format: "carousel", Title: "Promo",
		Social: &spec.Social{Caption: "Diskon jeruk."},
		Content: spec.Content{Sections: []spec.Section{{
			Type: spec.SectionPromo, Badge: "CRAZY DEAL", Title: "Jeruk Sunkist Cara Cara",
			ImageID: "img-1", Was: &spec.Cell{V: 5980, Fmt: "currency"},
			Price: &spec.Cell{V: 3370, Fmt: "currency"}, Unit: "/100 gram",
			Text: "Berlaku akhir pekan.",
		}}},
	}
	doc.Normalize()
	if err := doc.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	p, err := BuildCarousel(doc, Options{Now: fixtureNow, Currency: "IDR",
		Images: map[string]PromoImage{"img-1": {PNG: []byte("PNGBYTES"), Aspect: 1.5, Alt: "Jeruk dibelah"}}})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	if len(p.Scenes) != 1 || p.Scenes[0].Kind != KindPromo {
		t.Fatalf("%d slides, first %q", len(p.Scenes), p.Scenes[0].Kind)
	}
	sc := p.Scenes[0]

	// **Never compacted.** A KPI card says "Rp 3,86 Miliar" because the exact
	// figure is in the table it summarises; a promotion card is the exact
	// figure, and "Rp 3,4 Ribu" is not a price anybody can pay.
	if sc.Price != "Rp 3.370" || sc.Was != "Rp 5.980" {
		t.Errorf("prices = %q / %q, want the exact figures", sc.Price, sc.Was)
	}
	if sc.Badge != "CRAZY DEAL" || sc.Unit != "/100 gram" {
		t.Errorf("badge=%q unit=%q", sc.Badge, sc.Unit)
	}
	if sc.Image == nil || !strings.HasPrefix(sc.Image.DataURI, "data:image/png;base64,") {
		t.Fatalf("image = %+v, want an inlined data URI", sc.Image)
	}
	if sc.Image.Aspect != 1.5 {
		t.Errorf("aspect = %v, want 1.5", sc.Image.Aspect)
	}
	// The palette rides only on plans that need it.
	if p.Brand.Promo == nil || p.Brand.Promo.Ground == "" {
		t.Error("no promo palette on a plan with a promo card")
	}
	// The alt text is the card's content, and for a promotion that is the
	// prices: a description that omits them describes a photograph.
	for _, want := range []string{"CRAZY DEAL", "Jeruk Sunkist Cara Cara", "Rp 5.980", "Rp 3.370", "/100 gram", "Jeruk dibelah"} {
		if !strings.Contains(sc.Alt, want) {
			t.Errorf("alt %q omits %q", sc.Alt, want)
		}
	}
}

// An unresolved photograph is a card without one, never a failed render.
func TestAPromoWithNoImageStillDraws(t *testing.T) {
	doc := &spec.Document{
		Format: "carousel", Title: "Promo",
		Content: spec.Content{Sections: []spec.Section{{
			Type: spec.SectionPromo, Title: "Jeruk", Price: &spec.Cell{V: 3370, Fmt: "currency"},
		}}},
	}
	doc.Normalize()
	p, err := BuildCarousel(doc, Options{Now: fixtureNow, Currency: "IDR"})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	if p.Scenes[0].Image != nil {
		t.Error("an image appeared from nowhere")
	}
	if p.Scenes[0].Price == "" {
		t.Error("the price is what the card is for and it is missing")
	}
}

// The palette is a function of the tenant's accent, so a shop with a green
// brand gets a green promotion rather than ours with their logo on it.
func TestThePromoPaletteFollowsTheTenantsAccent(t *testing.T) {
	green := theme.Color{R: 0x18, G: 0x9A, B: 0x4D}
	doc := &spec.Document{
		Format: "carousel", Title: "Promo",
		Content: spec.Content{Sections: []spec.Section{{
			Type: spec.SectionPromo, Title: "Jeruk", Price: &spec.Cell{V: 100, Fmt: "currency"},
		}}},
	}
	doc.Normalize()
	p, err := BuildCarousel(doc, Options{Now: fixtureNow, Brand: BrandInput{Primary: &green}})
	if err != nil {
		t.Fatalf("BuildCarousel: %v", err)
	}
	if p.Brand.Promo.Ground != green.Hex() {
		t.Errorf("ground = %q, want the tenant's accent %q", p.Brand.Promo.Ground, green.Hex())
	}
	// Five distinct roles, and the two loudest are deliberately the same
	// colour: a shopper's eye travels from the badge to the price.
	if p.Brand.Promo.Badge != p.Brand.Promo.PriceBlock {
		t.Error("the badge and the price panel are different colours")
	}
	if p.Brand.Promo.Ray == p.Brand.Promo.Ground || p.Brand.Promo.Burst == p.Brand.Promo.Ground {
		t.Error("the sunburst has no contrast against its own ground")
	}
}

// No promo, no palette: every plan built before T-G12 carries the same brand
// block it did, which is what keeps the goldens byte-identical.
func TestAPlanWithNoPromoCarriesNoPromoPalette(t *testing.T) {
	p, err := BuildCarousel(loadCarouselFixture(t), Options{Now: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	if p.Brand.Promo != nil {
		t.Errorf("promo palette on a plan with no promo card: %+v", p.Brand.Promo)
	}
}
