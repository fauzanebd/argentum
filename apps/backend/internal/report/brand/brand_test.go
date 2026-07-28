package brand

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The zero Input is what a company that has never opened Settings → Reports
// resolves to, and it has to be exactly what the renderers did before branding
// existed.
func TestZeroInputIsTheArgentumDefault(t *testing.T) {
	c := Resolve(Input{})
	if c.Primary != theme.ColorPrimary {
		t.Errorf("primary = %s, want the brand red %s", c.Primary.Hex(), theme.ColorPrimary.Hex())
	}
	if c.Name != "" || c.FooterNote != "" || c.Confidentiality != "" || c.Locale != "" {
		t.Errorf("zero input produced content: %+v", c)
	}
	if len(c.LogoPNG) != 0 {
		t.Error("zero input produced a logo")
	}
}

// The rule this package exists for: each field falls back on its own. A tenant
// with a logo and no colour keeps our red rather than losing their logo.
func TestFallbacksArePerField(t *testing.T) {
	logo := []byte{0x89, 'P', 'N', 'G'}
	c := Resolve(Input{CompanyName: "Contoh", LogoPNG: logo})
	if c.Primary != theme.ColorPrimary {
		t.Errorf("primary = %s, want the default", c.Primary.Hex())
	}
	if string(c.LogoPNG) != string(logo) {
		t.Error("logo was dropped")
	}
	if c.Name != "Contoh" {
		t.Errorf("name = %q, want the company name", c.Name)
	}
}

func TestLegalNameWinsOverCompanyName(t *testing.T) {
	c := Resolve(Input{CompanyName: "Contoh", LegalName: "PT Contoh Sejahtera Tbk"})
	if c.Name != "PT Contoh Sejahtera Tbk" {
		t.Errorf("name = %q, want the legal name", c.Name)
	}
	// A legal name of spaces is not a legal name.
	if got := Resolve(Input{CompanyName: "Contoh", LegalName: "   "}).Name; got != "Contoh" {
		t.Errorf("name = %q, want the company name to survive a blank legal name", got)
	}
}

// The colour is validated when it is saved, so an unparseable one here means
// the row was edited outside the API. A document in the wrong red beats a
// document that does not render.
func TestUnparseableColourFallsBackRatherThanFailing(t *testing.T) {
	for _, bad := range []string{"red", "#FFF", "#GGGGGG", "12345"} {
		if got := Resolve(Input{PrimaryHex: bad}).Primary; got != theme.ColorPrimary {
			t.Errorf("PrimaryHex=%q resolved to %s, want the default", bad, got.Hex())
		}
	}
	if got := Resolve(Input{PrimaryHex: "#1C3A62"}).Primary.Hex(); got != "#1C3A62" {
		t.Errorf("valid colour resolved to %s", got)
	}
}

// ShowCredit is stored positively and consumed negatively; a mapping that lost
// the inversion would silently strip our mark from every document.
func TestCreditInversionSurvivesTheProjection(t *testing.T) {
	shown := Resolve(Input{ShowCredit: true})
	if shown.PDF().HideCredit || shown.PPTX().HideCredit {
		t.Error("ShowCredit=true produced HideCredit=true")
	}
	hidden := Resolve(Input{ShowCredit: false})
	if !hidden.PDF().HideCredit || !hidden.PPTX().HideCredit {
		t.Error("ShowCredit=false did not hide the credit")
	}
}

// The two projections are the same facts. A field added to one and forgotten in
// the other is how a report and the deck attached to it start disagreeing.
func TestProjectionsAgree(t *testing.T) {
	in := Input{
		CompanyName:          "Contoh",
		LegalName:            "PT Contoh Sejahtera",
		PrimaryHex:           "#1C3A62",
		LogoPNG:              []byte{0x89, 'P', 'N', 'G'},
		FooterText:           "© 2026 Contoh",
		ConfidentialityLabel: "Internal",
		ShowCredit:           false,
	}
	c := Resolve(in)
	p, d := c.PDF(), c.PPTX()

	if p.Name != d.Name || p.Confidentiality != d.Confidentiality ||
		p.FooterNote != d.FooterNote || p.HideCredit != d.HideCredit ||
		string(p.LogoPNG) != string(d.LogoPNG) {
		t.Errorf("projections differ:\n pdf  %+v\n pptx %+v", p, d)
	}
	if p.Primary == nil || d.Primary == nil || *p.Primary != *d.Primary {
		t.Error("projections disagree about the accent colour")
	}
	// Each projection owns its pointer: a renderer that wrote through it must
	// not reach the other format's options.
	if p.Primary == d.Primary {
		t.Error("both projections share one *theme.Color")
	}
}
