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
	for _, want := range []string{"a carousel is 2–10 slides", "at least 13", "merge or drop sections"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}
	if _, err := BuildCarousel(doc, Options{Now: fixtureNow}); err == nil || !strings.Contains(err.Error(), "a carousel is 2–10 slides") {
		t.Errorf("BuildCarousel: %v, want the slide-band refusal", err)
	}
}

// TestOneSlideIsRefused: a spec whose only section titles nothing produces the
// closing slide alone, and a one-slide carousel is not a carousel.
func TestOneSlideIsRefused(t *testing.T) {
	doc := &spec.Document{
		Title: "Nothing here",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHeading, Level: 2, Text: "A sub-heading with nothing under it"},
		}},
	}
	doc.Normalize()

	err := CheckCarouselLimits(doc, Options{Now: fixtureNow})
	if err == nil {
		t.Fatal("a one-slide spec was accepted as a carousel")
	}
	if !strings.Contains(err.Error(), "this spec makes 1") {
		t.Errorf("refusal %q does not state the count", err)
	}
	if _, err := BuildCarousel(doc, Options{Now: fixtureNow}); err == nil {
		t.Error("BuildCarousel accepted a one-slide spec")
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
	if !strings.Contains(err.Error(), "a carousel is 2–10 slides; this spec makes ") || strings.Contains(err.Error(), "at least") {
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
	if l := (Limits{}).Normalize(); l.MinSlides != 2 || l.MaxSlides != 10 {
		t.Errorf("zero limits normalise to %d–%d, want 2–10", l.MinSlides, l.MaxSlides)
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
