// Package brand resolves a tenant's configured branding into the shape the
// renderers take.
//
// It exists so that the fallback rules are written once. A tenant with a logo
// and no colour gets their logo and Argentum's red; a tenant with a legal name
// and no logo gets their name as a wordmark. Those decisions are per field, and
// duplicating them per format is how the PDF and the deck end up disagreeing
// about whose document this is.
//
// Deliberately free of internal/domain, like every other package under
// internal/report: the stored record is the caller's vocabulary, and mapping it
// into Input is one struct literal at the call site (see internal/app).
package brand

import (
	"strings"

	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/pptx"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Input is the raw material: what the company row knows, plus whatever the
// tenant configured in Settings → Reports.
type Input struct {
	// CompanyName is the workspace name, used when no legal name is set.
	CompanyName string

	// LegalName overrides CompanyName on the document and in its metadata.
	LegalName string

	// PrimaryHex is `#RRGGBB`. An unparseable value falls back to the brand
	// red rather than failing the render: it was validated when it was saved,
	// so reaching here means the row was edited outside the API, and a
	// document that renders in the wrong red beats a document that does not
	// render.
	PrimaryHex string

	// LogoPNG is the decoded logo. The caller reads it from object storage —
	// this package does no I/O, which is what keeps it testable and what stops
	// a slow bucket from being a render failure it has to model.
	LogoPNG []byte

	FooterText           string
	Locale               string
	ConfidentialityLabel string

	// ShowCredit is the resolved three-state from the stored record: a tenant
	// who never configured branding shows the credit.
	ShowCredit bool
}

// Config is Input with every fallback applied. It is what the renderers see.
type Config struct {
	Name            string
	LogoPNG         []byte
	Primary         theme.Color
	FooterNote      string
	Locale          string
	Confidentiality string
	ShowCredit      bool
}

// Resolve applies the per-field fallbacks. The zero Input resolves to
// Argentum's own defaults, which is the branding every company had before this
// existed.
func Resolve(in Input) Config {
	c := Config{
		Primary:         theme.ColorPrimary,
		Name:            strings.TrimSpace(firstNonEmpty(in.LegalName, in.CompanyName)),
		LogoPNG:         in.LogoPNG,
		FooterNote:      strings.TrimSpace(in.FooterText),
		Locale:          strings.ToLower(strings.TrimSpace(in.Locale)),
		Confidentiality: strings.TrimSpace(in.ConfidentialityLabel),
		ShowCredit:      in.ShowCredit,
	}
	if hex := strings.TrimSpace(in.PrimaryHex); hex != "" {
		if col, err := theme.ParseHexColor(hex); err == nil {
			c.Primary = col
		}
	}
	return c
}

// PDF projects the resolved branding onto the document renderer's options.
func (c Config) PDF() pdf.Brand {
	primary := c.Primary
	return pdf.Brand{
		Name:            c.Name,
		LogoPNG:         c.LogoPNG,
		Primary:         &primary,
		Confidentiality: c.Confidentiality,
		FooterNote:      c.FooterNote,
		HideCredit:      !c.ShowCredit,
	}
}

// PPTX is PDF for the deck. The two Brand types stay separate because the
// renderers are — and identical field for field because this is one set of
// facts about a tenant.
func (c Config) PPTX() pptx.Brand {
	primary := c.Primary
	return pptx.Brand{
		Name:            c.Name,
		LogoPNG:         c.LogoPNG,
		Primary:         &primary,
		Confidentiality: c.Confidentiality,
		FooterNote:      c.FooterNote,
		HideCredit:      !c.ShowCredit,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
