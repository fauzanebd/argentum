package pptx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

func brandLogo(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: 0x1C, G: 0x3A, B: 0x62, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode logo: %v", err)
	}
	return buf.Bytes()
}

func renderBranded(t *testing.T, fixture string, b Brand) *deck {
	t.Helper()
	out, err := Render(loadFixture(t, fixture), Options{Brand: b, Now: fixtureNow})
	if err != nil {
		t.Fatalf("render %s: %v", fixture, err)
	}
	return openDeck(t, out)
}

// One media part, referenced from the light slides only.
//
// One part rather than one per slide is the whole point: a 40 KB mark on the
// 50-slide export would otherwise be two megabytes of the same bytes. And the
// dark slides are excluded because a logo is supplied as one file, almost
// always dark ink on transparency, which is invisible on a near-black cover.
func TestLogoIsOnePartOnTheLightSlides(t *testing.T) {
	d := renderBranded(t, "monthly_sales.json", Brand{Name: "Contoh", LogoPNG: brandLogo(t, 240, 60)})

	if !d.has("ppt/media/logo.png") {
		t.Fatalf("no logo part in the package: %v", d.names)
	}
	logos := 0
	for _, n := range d.names {
		if strings.Contains(n, "logo") {
			logos++
		}
	}
	if logos != 1 {
		t.Errorf("%d logo parts in the package, want 1", logos)
	}

	slides := d.slideNames()
	referencing := 0
	for i := range slides {
		rels := d.xml(relsNameFor(i + 1))
		if strings.Contains(rels, "../media/logo.png") {
			referencing++
		}
	}
	if referencing == 0 {
		t.Error("no slide references the logo")
	}
	if referencing == len(slides) {
		t.Error("every slide references the logo, including the dark ones")
	}

	// Slide 1 is the cover and must not carry it.
	if strings.Contains(d.xml(relsNameFor(1)), "../media/logo.png") {
		t.Error("the dark cover references the logo")
	}
}

// A deck with no logo must contain no logo part and no dangling relationship —
// the failure mode a conditional part is most likely to produce.
func TestNoLogoLeavesNoTrace(t *testing.T) {
	d := renderBranded(t, "monthly_sales.json", Brand{Name: "Contoh"})
	for _, n := range d.names {
		if strings.Contains(n, "logo") {
			t.Errorf("unbranded deck contains %s", n)
		}
	}
	for i := range d.slideNames() {
		if strings.Contains(d.xml(relsNameFor(i+1)), "logo") {
			t.Errorf("slide %d references a logo that is not in the package", i+1)
		}
	}
}

// The accent has to be lifted on the dark slides and left alone on the light
// ones. A navy that reads perfectly on paper is invisible on #0A0A0A, and
// rejecting it at configuration time would be fixing the wrong end.
func TestAccentIsLiftedOnDarkSlidesOnly(t *testing.T) {
	navy := theme.Color{R: 0x1C, G: 0x3A, B: 0x62}
	d := renderBranded(t, "monthly_sales.json", Brand{Name: "Contoh", Primary: &navy})

	cover := d.xml("ppt/slides/slide1.xml")
	if strings.Contains(cover, hexRGB(navy)) {
		t.Errorf("the cover draws the raw navy %s, which is unreadable on the dark ground", hexRGB(navy))
	}
	lifted := theme.Readable(navy, theme.ColorForeground, theme.White, theme.MinBrandContrast)
	if !strings.Contains(cover, hexRGB(lifted)) {
		t.Errorf("the cover does not draw the lifted accent %s", hexRGB(lifted))
	}

	// A content slide keeps the tenant's colour exactly as configured.
	var found bool
	for _, n := range d.slideNames() {
		if strings.Contains(d.xml(n), hexRGB(navy)) {
			found = true
			break
		}
	}
	if !found {
		t.Error("no content slide draws the configured accent")
	}

	// And the Argentum red is gone from the deck entirely.
	for _, n := range d.slideNames() {
		if strings.Contains(d.xml(n), hexRGB(theme.ColorPrimary)) {
			t.Errorf("%s still draws the Argentum red", n)
		}
	}
}

func TestDeckCreditIsOnByDefaultAndRemovable(t *testing.T) {
	with := renderBranded(t, "kpi_summary.json", Brand{Name: "Contoh"})
	if !strings.Contains(strings.Join(partsWithPrefix(with, "ppt/slides/slide"), ""), "Made with Argentum") {
		t.Error("the credit is missing from a deck that did not opt out")
	}
	without := renderBranded(t, "kpi_summary.json", Brand{Name: "Contoh", HideCredit: true})
	if strings.Contains(strings.Join(partsWithPrefix(without, "ppt/slides/slide"), ""), "Argentum") {
		t.Error("the credit survived HideCredit")
	}
}

// Branding must not cost determinism: the logo part and its relationships are
// assigned by position, so two renders of one branded spec are one file.
func TestBrandedDeckIsStillDeterministic(t *testing.T) {
	b := Brand{Name: "Contoh", LogoPNG: brandLogo(t, 240, 60)}
	doc := loadFixture(t, "monthly_sales.json")

	first, err := Render(doc, Options{Brand: b, Now: fixtureNow})
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := Render(doc, Options{Brand: b, Now: fixtureNow})
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two renders differ: %d vs %d bytes", len(first), len(second))
	}
}

func relsNameFor(slide int) string {
	return "ppt/slides/_rels/slide" + strconv.Itoa(slide) + ".xml.rels"
}
