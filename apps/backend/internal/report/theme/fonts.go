package theme

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/johnfercher/maroto/v2/pkg/repository"
)

// Space Grotesk, vendored under fonts/ with its OFL licence.
//
// The dashboard loads this family from the Google Fonts CDN. A Go renderer
// cannot: maroto needs the TTF bytes at construction time, and a worker with no
// egress still has to produce a document. So the faces are embedded, which also
// makes them a build-time dependency — delete one and the compiler says so,
// rather than the renderer silently falling back to Helvetica, which is the
// exact regression this package exists to remove.
//
// Three faces, matching what the dashboard uses: 400, 500, 700.
var (
	//go:embed fonts/SpaceGrotesk-Regular.ttf
	fontRegular []byte

	//go:embed fonts/SpaceGrotesk-Medium.ttf
	fontMedium []byte

	//go:embed fonts/SpaceGrotesk-Bold.ttf
	fontBold []byte
)

// face is one registration: a maroto family key, a style, and the bytes.
type face struct {
	family string
	style  fontstyle.Type
	name   string
	bytes  []byte
}

// faces enumerates every (family, style) pair the renderer may ask for.
//
// Two things to know about the mapping:
//
//   - Space Grotesk ships no italic. gofpdf, underneath maroto, errors on a
//     style it has no font for, so italic is registered pointing at the upright
//     bytes. An upright glyph where italic was requested is a design compromise
//     the family forces; a failed render would not be.
//   - maroto's style axis is normal/bold/italic only, so weight 500 cannot be a
//     style of the same family. It is registered as its own family instead —
//     FontMedium — which is why table headers name a family rather than a
//     weight.
func faces() []face {
	return []face{
		{FontBody, fontstyle.Normal, "SpaceGrotesk-Regular", fontRegular},
		{FontBody, fontstyle.Bold, "SpaceGrotesk-Bold", fontBold},
		{FontBody, fontstyle.Italic, "SpaceGrotesk-Regular (no italic face exists)", fontRegular},
		{FontBody, fontstyle.BoldItalic, "SpaceGrotesk-Bold (no italic face exists)", fontBold},
		{FontMedium, fontstyle.Normal, "SpaceGrotesk-Medium", fontMedium},
		{FontMedium, fontstyle.Bold, "SpaceGrotesk-Bold", fontBold},
	}
}

// ttfMagic is the sfnt version tag of a TrueType outline font (0x00010000).
// OpenType/CFF fonts start with "OTTO" and TrueType collections with "ttcf";
// gofpdf parses neither, so accepting only this tag is the point.
var ttfMagic = []byte{0x00, 0x01, 0x00, 0x00}

// VerifyFonts checks every embedded face before anything tries to render with
// it. Call it at process start: a font problem should stop a boot, not surface
// as a failed document three hours later in front of a customer.
//
// A missing file is already a compile error, courtesy of the embed directives
// above. What remains for runtime is a face that exists but is not a parseable
// TrueType font — a truncated download, a Git LFS pointer committed by
// accident, an OTF renamed to .ttf.
func VerifyFonts() error {
	for _, f := range faces() {
		if err := verifyFace(f); err != nil {
			return err
		}
	}
	return nil
}

func verifyFace(f face) error {
	if len(f.bytes) == 0 {
		return fmt.Errorf("theme: font face %s (%s/%q) is empty", f.name, f.family, f.style)
	}
	if len(f.bytes) < len(ttfMagic) || !bytes.Equal(f.bytes[:len(ttfMagic)], ttfMagic) {
		return fmt.Errorf(
			"theme: font face %s (%s/%q) is not a TrueType font: got magic %x, want %x",
			f.name, f.family, f.style, f.bytes[:min(len(f.bytes), len(ttfMagic))], ttfMagic)
	}
	return nil
}

// CustomFonts returns the faces in the form maroto's config takes.
func CustomFonts() ([]*entity.CustomFont, error) {
	if err := VerifyFonts(); err != nil {
		return nil, err
	}
	repo := repository.New()
	for _, f := range faces() {
		repo.AddUTF8FontFromBytes(f.family, f.style, f.bytes)
	}
	fonts, err := repo.Load()
	if err != nil {
		return nil, fmt.Errorf("theme: load fonts: %w", err)
	}
	return fonts, nil
}

// ConfigBuilder is the document baseline every PDF renders against: A4, the
// theme's margins, the embedded family as the default font, body text at the
// type scale's body size in the theme's foreground colour, and the fine grid.
//
// It returns the builder rather than the built config because the parts a
// document differs on — its title, its author, its page-number pattern, when
// it was generated — are the renderer's business and not the design system's.
// Anything the renderer does not add comes from here, so a section that forgets
// to name a size or a colour still lands inside the design system.
func ConfigBuilder() (config.Builder, error) {
	fonts, err := CustomFonts()
	if err != nil {
		return nil, err
	}
	return config.NewBuilder().
		WithPageSize(pagesize.A4).
		WithLeftMargin(Page.Margin).
		WithRightMargin(Page.Margin).
		WithTopMargin(Page.Margin).
		WithBottomMargin(Page.Margin).
		WithMaxGridSize(GridCols).
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{
			Family: FontBody,
			Style:  fontstyle.Normal,
			Size:   TypeScale.Body,
			Color:  ColorForeground.Props(),
		}), nil
}

// MarotoConfig is ConfigBuilder built, for callers with nothing to add.
func MarotoConfig() (*entity.Config, error) {
	b, err := ConfigBuilder()
	if err != nil {
		return nil, err
	}
	return b.Build(), nil
}
