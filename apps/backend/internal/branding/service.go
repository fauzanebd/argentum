// Package branding owns the tenant's report identity: reading it, validating
// it, storing the logo, and resolving all of it into what the renderers take.
//
// It is its own package rather than a method set on app.CompanyService because
// it has two callers that must not diverge — the HTTP handler that saves and
// previews branding, and the generate_document tool that renders with it. A
// second resolver in the tool is how "the preview looks right and the real
// document does not" happens.
package branding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder for image.Decode; PNG is the only encoder
	"image/png"
	"io"
	"strings"

	"golang.org/x/image/draw"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/brand"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// ObjectStore is the slice of the storage adapter this package needs.
// Declared here, and narrow, so the service can be tested without MinIO.
type ObjectStore interface {
	UploadKey(ctx context.Context, key string, r io.Reader, contentType string) (string, error)
	DownloadKey(ctx context.Context, key string) ([]byte, error)
}

// Limits on an uploaded logo.
const (
	// MaxLogoBytes is what the endpoint accepts. A logo is a mark, not a
	// photograph: half a megabyte is generous for one, and the cap is what
	// stops the renderer having to think about how long a download takes.
	MaxLogoBytes = 512 << 10

	// MaxLogoEdge is the long edge after re-encoding. The mark is drawn at
	// about 40mm on a cover and 32mm in a deck footer, so 2000px is already
	// far past what any output resolution can use; anything larger is
	// downscaled rather than rejected, because "your file is too big" is a
	// worse answer than a correctly sized logo.
	MaxLogoEdge = 2000
)

// LogoKey is where a company's mark lives. One key per company, overwritten on
// re-upload: versioning a logo would mean the renderer had to be told which
// version to draw, and nobody has ever wanted the old one.
func LogoKey(companyID string) string {
	return "branding/" + companyID + "/logo.png"
}

// Service reads and writes the branding record.
type Service struct {
	repo    domain.BrandingRepository
	store   ObjectStore
	company domain.CompanyRepository
}

func NewService(repo domain.BrandingRepository, store ObjectStore, company domain.CompanyRepository) *Service {
	return &Service{repo: repo, store: store, company: company}
}

// Get returns the stored record. A company that has never configured branding
// gets the zero value, not an error and not nil.
func (s *Service) Get(ctx context.Context, companyID string) (*domain.ReportBranding, error) {
	if s == nil || s.repo == nil {
		return &domain.ReportBranding{}, nil
	}
	b, err := s.repo.GetBranding(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		b = &domain.ReportBranding{}
	}
	return b, nil
}

// Save validates and stores. It returns the stored record so the caller does
// not have to guess what normalisation did.
func (s *Service) Save(ctx context.Context, companyID string, in *domain.ReportBranding) (*domain.ReportBranding, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("branding: service not configured")
	}
	if in == nil {
		in = &domain.ReportBranding{}
	}
	in.Normalize()
	if err := Validate(in); err != nil {
		return nil, err
	}
	if err := s.repo.SaveBranding(ctx, companyID, in); err != nil {
		return nil, err
	}
	return in, nil
}

// Validate is the whole contract for a branding record. It is exported because
// the preview endpoint checks a record it is never going to store.
func Validate(b *domain.ReportBranding) error {
	if b == nil {
		return nil
	}
	if b.PrimaryColor != "" {
		if err := ValidateColor(b.PrimaryColor); err != nil {
			return err
		}
	}
	switch b.Locale {
	case "", "en", "id":
	default:
		return fmt.Errorf("%w: locale must be \"en\" or \"id\", got %q", domain.ErrInvalidInput, b.Locale)
	}
	// The footer note and the confidentiality label are drawn on one line of a
	// footer that is already carrying a timestamp. Past roughly this length
	// they are silently truncated by the renderer, and a limit the customer
	// can see beats a truncation they cannot.
	if len(b.FooterText) > 120 {
		return fmt.Errorf("%w: footer text must be 120 characters or fewer (got %d)",
			domain.ErrInvalidInput, len(b.FooterText))
	}
	if len(b.ConfidentialityLabel) > 40 {
		return fmt.Errorf("%w: confidentiality label must be 40 characters or fewer (got %d)",
			domain.ErrInvalidInput, len(b.ConfidentialityLabel))
	}
	if len(b.LegalName) > 120 {
		return fmt.Errorf("%w: legal name must be 120 characters or fewer (got %d)",
			domain.ErrInvalidInput, len(b.LegalName))
	}
	return nil
}

// ValidateColor rejects an accent that cannot be read on paper, and says by how
// much. The measured ratio is in the message on purpose: "too light" sends a
// customer back to their brand guideline with nothing to act on, and
// "2.4:1, needs 3:1" tells them how far off they are.
func ValidateColor(hex string) error {
	c, err := theme.ParseHexColor(hex)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}
	if ratio := theme.ContrastRatio(c, theme.White); ratio < theme.MinBrandContrast {
		return fmt.Errorf("%w: %s has %.2f:1 contrast against white and needs at least %.1f:1 — "+
			"it would be unreadable as a heading or a rule on a printed page",
			domain.ErrInvalidInput, strings.ToUpper(hex), ratio, theme.MinBrandContrast)
	}
	return nil
}

// UploadLogo re-encodes an uploaded image and stores it as the company's mark,
// returning the key to put in the branding record.
//
// Re-encoding is the point, not a side effect. It strips EXIF (which can carry
// the photographer's location and, in a logo, is pure leakage), it normalises
// JPEG to the PNG the renderers require, and it means the bytes the renderer
// draws were produced by Go's encoder from a decoded image rather than by
// whatever produced the upload — so a malformed file fails here, in a handler,
// instead of inside a document render two days later.
func (s *Service) UploadLogo(ctx context.Context, companyID string, r io.Reader) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("branding: object storage is not configured")
	}

	// One byte past the limit is enough to know it is over it, and reading
	// exactly that much means a 50 MB upload costs 512 KB of memory.
	raw, err := io.ReadAll(io.LimitReader(r, MaxLogoBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: could not read the upload: %s", domain.ErrInvalidInput, err.Error())
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: the upload is empty", domain.ErrInvalidInput)
	}
	if len(raw) > MaxLogoBytes {
		return "", fmt.Errorf("%w: the logo must be %d KB or smaller", domain.ErrInvalidInput, MaxLogoBytes>>10)
	}

	encoded, err := NormalizeLogo(raw)
	if err != nil {
		return "", err
	}
	key := LogoKey(companyID)
	if _, err := s.store.UploadKey(ctx, key, bytes.NewReader(encoded), "image/png"); err != nil {
		return "", err
	}
	return key, nil
}

// NormalizeLogo decodes, downscales if needed, and re-encodes as PNG.
// Exported for the tests that pin the format and size rules.
func NormalizeLogo(raw []byte) ([]byte, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		// image.Decode's own error ("image: unknown format") tells a customer
		// nothing about what they should have uploaded.
		return nil, fmt.Errorf("%w: the logo must be a PNG or JPEG image", domain.ErrInvalidInput)
	}
	switch format {
	case "png", "jpeg":
	default:
		// Registered decoders are png and jpeg only (see the imports), so this
		// is unreachable today and is here so that adding a decoder somewhere
		// else in the binary cannot widen what this endpoint accepts. SVG in
		// particular is a script-injection surface in a document renderer.
		return nil, fmt.Errorf("%w: the logo must be a PNG or JPEG image, got %s",
			domain.ErrInvalidInput, format)
	}

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("%w: the logo has no pixels", domain.ErrInvalidInput)
	}
	if b.Dx() > MaxLogoEdge || b.Dy() > MaxLogoEdge {
		img = downscale(img, MaxLogoEdge)
	}

	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("re-encode logo: %w", err)
	}
	return out.Bytes(), nil
}

// downscale fits the image inside a max×max box, keeping its aspect ratio.
// CatmullRom rather than nearest-neighbour: a logo is mostly edges, and edges
// are what the cheap kernels destroy.
func downscale(src image.Image, max int) image.Image {
	b := src.Bounds()
	scale := float64(max) / float64(b.Dx())
	if s := float64(max) / float64(b.Dy()); s < scale {
		scale = s
	}
	w := int(float64(b.Dx())*scale + 0.5)
	h := int(float64(b.Dy())*scale + 0.5)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// Resolve reads the branding record and the logo and applies the fallbacks,
// producing what a renderer takes. This is the one path both the preview
// endpoint and the generate_document tool go through.
//
// Nothing here is fatal. A missing company, an unreadable branding row or a
// logo the bucket has lost all resolve to Argentum's defaults, because a
// document that renders unbranded is worth more than an error where a report
// was asked for. The caller passes onErr to record what was skipped.
func (s *Service) Resolve(ctx context.Context, companyID string, onErr func(error)) brand.Config {
	if s == nil || s.repo == nil {
		return s.resolveWith(ctx, companyID, nil, onErr)
	}
	rec, err := s.repo.GetBranding(ctx, companyID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) && onErr != nil {
			onErr(fmt.Errorf("branding: record lookup failed: %w", err))
		}
		rec = nil
	}
	return s.resolveWith(ctx, companyID, rec, onErr)
}

// Preview resolves a record the caller holds but has not saved. It is the same
// code path as Resolve on purpose: a preview produced by a second resolver is a
// preview that can be right while the document is wrong, which is the one thing
// a preview must never be.
func (s *Service) Preview(ctx context.Context, companyID string, rec *domain.ReportBranding, onErr func(error)) brand.Config {
	return s.resolveWith(ctx, companyID, rec, onErr)
}

func (s *Service) resolveWith(ctx context.Context, companyID string, rec *domain.ReportBranding, onErr func(error)) brand.Config {
	report := func(err error) {
		if err != nil && onErr != nil {
			onErr(err)
		}
	}

	in := brand.Input{ShowCredit: true}
	if s == nil {
		return brand.Resolve(in)
	}

	if s.company != nil {
		company, err := s.company.GetByID(ctx, companyID)
		switch {
		case err != nil:
			report(fmt.Errorf("branding: company lookup failed: %w", err))
		case company != nil:
			in.CompanyName = company.Name
		}
	}

	if rec == nil {
		return brand.Resolve(in)
	}

	in.LegalName = rec.LegalName
	in.PrimaryHex = rec.PrimaryColor
	in.FooterText = rec.FooterText
	in.Locale = rec.Locale
	in.ConfidentialityLabel = rec.ConfidentialityLabel
	in.ShowCredit = rec.CreditVisible()

	if rec.LogoKey != "" && s.store != nil {
		logo, err := s.store.DownloadKey(ctx, rec.LogoKey)
		if err != nil {
			report(fmt.Errorf("branding: logo %q unreadable: %w", rec.LogoKey, err))
		} else {
			in.LogoPNG = logo
		}
	}
	return brand.Resolve(in)
}
