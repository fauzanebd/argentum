package pdf

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnfercher/go-tree/node"
	"github.com/johnfercher/maroto/v2/pkg/core"

	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// A branded document has to actually say the tenant's name where the wordmark
// goes — on the cover and in the running header — rather than Argentum's.
func TestTenantWordmarkReplacesArgentum(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")

	plain := pageTexts(t, doc, Options{})
	if !hasExactly(plain[0], "Argentum") {
		t.Fatalf("unbranded cover does not carry the Argentum wordmark: %v", plain[0])
	}

	branded := pageTexts(t, doc, Options{Brand: Brand{Name: "PT Contoh Sejahtera"}})
	if !contains(branded[0], "PT Contoh Sejahtera") {
		t.Errorf("branded cover missing the tenant name: %v", branded[0])
	}
	// The wordmark is a text run that *is* "Argentum"; the footer credit is a
	// run that merely contains it. Matching on substring here would pass a
	// document that still drew our mark in the header beside a tenant's name.
	for i, page := range branded {
		if hasExactly(page, "Argentum") {
			t.Errorf("page %d still carries the Argentum wordmark: %v", i+1, page)
		}
	}
}

func hasExactly(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.TrimSpace(s) == needle {
			return true
		}
	}
	return false
}

// The accent has to reach every rule and every piece of brand-coloured text.
// Asserting on the *structure* rather than the bytes is what makes this a test
// of the fix rather than of one call site: a new rule drawn straight from
// theme.ColorPrimary changes the count here.
func TestAccentReachesEveryBrandColouredElement(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")

	tenant := theme.Color{R: 0x1C, G: 0x3A, B: 0x62}
	def := renderColors(t, doc, Options{})
	branded := renderColors(t, doc, Options{Brand: Brand{Name: "Contoh", Primary: &tenant}})

	red := rgbKey(theme.ColorPrimary)
	if def[red] == 0 {
		t.Fatal("the unbranded document draws nothing in the brand red")
	}
	if branded[red] != 0 {
		t.Errorf("%d elements still drawn in Argentum red after branding", branded[red])
	}
	if got, want := branded[rgbKey(tenant)], def[red]; got != want {
		t.Errorf("tenant accent drawn %d times, brand red was drawn %d", got, want)
	}
}

// renderColors counts the drawn elements carrying each colour.
//
// It reads maroto's own structure tree rather than the finished bytes for the
// reason pageTexts gives about text: a content stream sets colour as an
// operator, so a PDF cannot tell a test which *rule* was drawn in which
// colour. Every component's props land in Details["prop_color"], which is
// exactly the value the renderer passed in.
func renderColors(t *testing.T, doc *spec.Document, opts Options) map[string]int {
	t.Helper()
	r, err := newRenderer(doc, opts)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	if err := r.build(); err != nil {
		t.Fatalf("build: %v", err)
	}
	counts := map[string]int{}
	collectColors(r.m.GetStructure(), counts)
	return counts
}

func collectColors(n *node.Node[core.Structure], out map[string]int) {
	if c, ok := n.GetData().Details["prop_color"].(string); ok && c != "" {
		out[c]++
	}
	for _, next := range n.GetNexts() {
		collectColors(next, out)
	}
}

// rgbKey is props.Color.ToString()'s format, which is what lands in the
// structure tree.
func rgbKey(c theme.Color) string {
	return fmt.Sprintf("RGB(%d, %d, %d)", c.R, c.G, c.B)
}

// The Argentum credit is on by default and removable, and removing it is the
// only difference it makes.
func TestCreditIsOnByDefaultAndRemovable(t *testing.T) {
	// An English fixture, because Options.Locale is only a fallback — a spec
	// that names its own locale wins, which is the right precedence and is why
	// pinning it here would not have worked. The wording is
	// TestCreditFollowsTheDocumentLocale's subject; this test is the flag.
	doc := loadFixture(t, "kpi_summary.json")

	with := pageTexts(t, doc, Options{Brand: Brand{Name: "Contoh"}})
	if !anyPageContains(with, "Made with Argentum") {
		t.Error("the credit is missing from a document that did not opt out")
	}

	without := pageTexts(t, doc, Options{Brand: Brand{Name: "Contoh", HideCredit: true}})
	if anyPageContains(without, "Made with Argentum") {
		t.Error("the credit survived HideCredit")
	}
	if len(with) != len(without) {
		t.Errorf("hiding the credit changed the page count: %d -> %d", len(with), len(without))
	}
}

// An Indonesian document says it in Indonesian, credit included: a footer that
// reads "Halaman 2 dari 17 · Made with Argentum" is the kind of half-translated
// artefact the labels package exists to prevent.
func TestCreditFollowsTheDocumentLocale(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	pages := pageTexts(t, doc, Options{Brand: Brand{Name: "Contoh"}, Locale: "id"})
	if !anyPageContains(pages, "Dibuat dengan Argentum") {
		t.Errorf("Indonesian document does not carry the Indonesian credit")
	}
	if anyPageContains(pages, "Made with Argentum") {
		t.Errorf("Indonesian document carries the English credit")
	}
}

func anyPageContains(pages [][]string, needle string) bool {
	for _, page := range pages {
		for _, s := range page {
			if strings.Contains(s, needle) {
				return true
			}
		}
	}
	return false
}
