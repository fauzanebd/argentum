package branding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

type fakeRepo struct {
	rec *domain.ReportBranding
	err error

	saved *domain.ReportBranding
}

func (f *fakeRepo) GetBranding(context.Context, string) (*domain.ReportBranding, error) {
	return f.rec, f.err
}

func (f *fakeRepo) SaveBranding(_ context.Context, _ string, b *domain.ReportBranding) error {
	f.saved = b
	return nil
}

type fakeCompanies struct {
	company *domain.Company
	err     error
}

func (f *fakeCompanies) Create(context.Context, *domain.Company) error { return nil }
func (f *fakeCompanies) GetByID(context.Context, string) (*domain.Company, error) {
	return f.company, f.err
}
func (f *fakeCompanies) GetBySlug(context.Context, string) (*domain.Company, error) {
	return f.company, f.err
}
func (f *fakeCompanies) Update(context.Context, *domain.Company) error { return nil }

type fakeStore struct {
	objects map[string][]byte
	getErr  error
	putErr  error
}

func newFakeStore() *fakeStore { return &fakeStore{objects: map[string][]byte{}} }

func (f *fakeStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	if f.putErr != nil {
		return "", f.putErr
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return "", err
	}
	f.objects[key] = buf.Bytes()
	return "http://storage.local/" + key, nil
}

func (f *fakeStore) DownloadKey(_ context.Context, key string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	b, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("no such object %q", key)
	}
	return b, nil
}

// --- validation -------------------------------------------------------------

// The message a customer reads when their brand colour is too pale has to carry
// the measured ratio: "too light" sends them back to a brand guideline with
// nothing to act on.
func TestLowContrastColourIsRejectedWithItsRatio(t *testing.T) {
	err := ValidateColor("#F5E9A0") // pale yellow
	if err == nil {
		t.Fatal("pale yellow was accepted")
	}
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("error does not wrap ErrInvalidInput: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, ":1") || !strings.Contains(msg, "3.0:1") {
		t.Errorf("message does not state the measured and required ratios: %q", msg)
	}
	if !strings.Contains(msg, "#F5E9A0") {
		t.Errorf("message does not name the colour: %q", msg)
	}
}

func TestBrandColoursThatWorkAreAccepted(t *testing.T) {
	for _, hex := range []string{
		theme.ColorPrimary.Hex(), // our own red
		"#1C3A62",                // navy
		"#000000",                // black
		"#713F1C",                // brown
	} {
		if err := ValidateColor(hex); err != nil {
			t.Errorf("ValidateColor(%s) = %v, want nil", hex, err)
		}
	}
}

func TestValidateRejectsUnknownLocaleAndOverlongText(t *testing.T) {
	if err := Validate(&domain.ReportBranding{Locale: "fr"}); err == nil {
		t.Error("an unsupported locale was accepted")
	}
	if err := Validate(&domain.ReportBranding{FooterText: strings.Repeat("x", 121)}); err == nil {
		t.Error("an overlong footer line was accepted")
	}
	if err := Validate(&domain.ReportBranding{ConfidentialityLabel: strings.Repeat("x", 41)}); err == nil {
		t.Error("an overlong confidentiality label was accepted")
	}
	if err := Validate(&domain.ReportBranding{}); err != nil {
		t.Errorf("the empty record was rejected: %v", err)
	}
}

func TestSaveNormalizesBeforeStoring(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, newFakeStore(), &fakeCompanies{})

	saved, err := svc.Save(context.Background(), "co-1", &domain.ReportBranding{
		PrimaryColor: " #1c3a62 ",
		LegalName:    "  PT Contoh  ",
		Locale:       "ID",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.PrimaryColor != "#1C3A62" {
		t.Errorf("colour stored as %q, want #1C3A62", saved.PrimaryColor)
	}
	if saved.LegalName != "PT Contoh" {
		t.Errorf("legal name stored as %q", saved.LegalName)
	}
	if saved.Locale != "id" {
		t.Errorf("locale stored as %q, want id", saved.Locale)
	}
	if repo.saved == nil {
		t.Error("nothing reached the repository")
	}
}

// --- logo -------------------------------------------------------------------

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: 0x5C, B: 0x5C, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}

// A JPEG has to come back as a PNG: the renderers take PNG only, and doing the
// conversion at upload is what keeps that a fact rather than a hope.
func TestUploadReencodesJPEGToPNG(t *testing.T) {
	store := newFakeStore()
	svc := NewService(&fakeRepo{}, store, &fakeCompanies{})

	key, err := svc.UploadLogo(context.Background(), "co-1", bytes.NewReader(testJPEG(t, 64, 32)))
	if err != nil {
		t.Fatalf("UploadLogo: %v", err)
	}
	if key != "branding/co-1/logo.png" {
		t.Errorf("key = %q", key)
	}
	stored := store.objects[key]
	if _, err := png.Decode(bytes.NewReader(stored)); err != nil {
		t.Errorf("stored object is not a PNG: %v", err)
	}
}

func TestOversizedImageIsScaledDownRatherThanRejected(t *testing.T) {
	out, err := NormalizeLogo(testPNG(t, MaxLogoEdge+800, (MaxLogoEdge+800)/2))
	if err != nil {
		t.Fatalf("NormalizeLogo: %v", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width > MaxLogoEdge || cfg.Height > MaxLogoEdge {
		t.Errorf("result is %dx%d, over the %dpx cap", cfg.Width, cfg.Height, MaxLogoEdge)
	}
	// 2:1 in, 2:1 out. A cap that reshapes a wordmark is worse than no cap.
	if got, want := float64(cfg.Width)/float64(cfg.Height), 2.0; got < want-0.01 || got > want+0.01 {
		t.Errorf("aspect ratio changed: %.3f, want %.3f", got, want)
	}
}

func TestNonImageUploadsAreRejected(t *testing.T) {
	svc := NewService(&fakeRepo{}, newFakeStore(), &fakeCompanies{})
	cases := map[string][]byte{
		// The one the ticket calls out: an SVG in a document renderer is a
		// script-injection surface, and it is not a raster image either.
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x()</script></svg>`),
		"html":      []byte("<!doctype html><p>hello"),
		"truncated": testPNG(t, 8, 8)[:20],
		"empty":     {},
	}
	for name, body := range cases {
		_, err := svc.UploadLogo(context.Background(), "co-1", bytes.NewReader(body))
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: error does not wrap ErrInvalidInput: %v", name, err)
		}
	}
}

func TestUploadRejectsOversizedBodies(t *testing.T) {
	svc := NewService(&fakeRepo{}, newFakeStore(), &fakeCompanies{})
	_, err := svc.UploadLogo(context.Background(), "co-1",
		bytes.NewReader(bytes.Repeat([]byte{0x42}, MaxLogoBytes+1)))
	if err == nil || !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("oversized upload: %v, want an invalid-input error", err)
	}
	if !strings.Contains(err.Error(), "KB") {
		t.Errorf("message does not state the limit: %q", err.Error())
	}
}

// --- resolve ----------------------------------------------------------------

func TestResolveReadsTheRecordAndTheLogo(t *testing.T) {
	store := newFakeStore()
	store.objects["branding/co-1/logo.png"] = testPNG(t, 8, 8)
	no := false
	svc := NewService(&fakeRepo{rec: &domain.ReportBranding{
		LogoKey:              "branding/co-1/logo.png",
		PrimaryColor:         "#1C3A62",
		LegalName:            "PT Contoh Sejahtera",
		FooterText:           "© 2026",
		Locale:               "id",
		ConfidentialityLabel: "Rahasia",
		ShowArgentumCredit:   &no,
	}}, store, &fakeCompanies{company: &domain.Company{Name: "Contoh"}})

	cfg := svc.Resolve(context.Background(), "co-1", func(err error) {
		t.Errorf("unexpected resolve error: %v", err)
	})

	if cfg.Name != "PT Contoh Sejahtera" {
		t.Errorf("name = %q", cfg.Name)
	}
	if cfg.Primary.Hex() != "#1C3A62" {
		t.Errorf("primary = %s", cfg.Primary.Hex())
	}
	if len(cfg.LogoPNG) == 0 {
		t.Error("logo was not read")
	}
	if cfg.ShowCredit {
		t.Error("credit is shown despite show_argentum_credit=false")
	}
	if cfg.Locale != "id" || cfg.Confidentiality != "Rahasia" {
		t.Errorf("locale/confidentiality = %q/%q", cfg.Locale, cfg.Confidentiality)
	}
}

// A company that has never configured branding must resolve to the defaults
// *including* the credit — the pointer's nil state means "never asked", not
// "opted out".
func TestUnconfiguredCompanyKeepsTheCredit(t *testing.T) {
	svc := NewService(&fakeRepo{rec: &domain.ReportBranding{}}, newFakeStore(),
		&fakeCompanies{company: &domain.Company{Name: "Contoh"}})

	cfg := svc.Resolve(context.Background(), "co-1", nil)
	if !cfg.ShowCredit {
		t.Error("an unconfigured company lost the credit")
	}
	if cfg.Primary != theme.ColorPrimary {
		t.Errorf("primary = %s, want the default", cfg.Primary.Hex())
	}
	if cfg.Name != "Contoh" {
		t.Errorf("name = %q, want the company name", cfg.Name)
	}
}

// Nothing about branding may fail a render. Each dependency is broken in turn
// and the document still gets a usable identity.
func TestResolveIsNeverFatal(t *testing.T) {
	logo := &domain.ReportBranding{LogoKey: "branding/co-1/logo.png", PrimaryColor: "#1C3A62"}

	t.Run("logo missing from the bucket", func(t *testing.T) {
		store := newFakeStore() // no object under the key
		svc := NewService(&fakeRepo{rec: logo}, store, &fakeCompanies{company: &domain.Company{Name: "Contoh"}})

		var reported error
		cfg := svc.Resolve(context.Background(), "co-1", func(err error) { reported = err })

		if len(cfg.LogoPNG) != 0 {
			t.Error("a logo appeared from nowhere")
		}
		if cfg.Primary.Hex() != "#1C3A62" {
			t.Error("the rest of the branding was discarded with the logo")
		}
		if reported == nil {
			t.Error("the unreadable logo was not reported")
		}
	})

	t.Run("branding row unreadable", func(t *testing.T) {
		svc := NewService(&fakeRepo{err: errors.New("column does not exist")}, newFakeStore(),
			&fakeCompanies{company: &domain.Company{Name: "Contoh"}})

		var reported error
		cfg := svc.Resolve(context.Background(), "co-1", func(err error) { reported = err })

		if cfg.Primary != theme.ColorPrimary || cfg.Name != "Contoh" || !cfg.ShowCredit {
			t.Errorf("a broken branding row did not fall back cleanly: %+v", cfg)
		}
		if reported == nil {
			t.Error("the failure was swallowed silently")
		}
	})

	t.Run("company lookup fails", func(t *testing.T) {
		svc := NewService(&fakeRepo{rec: &domain.ReportBranding{PrimaryColor: "#1C3A62"}},
			newFakeStore(), &fakeCompanies{err: errors.New("connection refused")})

		cfg := svc.Resolve(context.Background(), "co-1", func(error) {})
		if cfg.Primary.Hex() != "#1C3A62" {
			t.Error("configured branding was lost with the company lookup")
		}
	})

	t.Run("nil service", func(t *testing.T) {
		var svc *Service
		cfg := svc.Resolve(context.Background(), "co-1", nil)
		if cfg.Primary != theme.ColorPrimary || !cfg.ShowCredit {
			t.Errorf("a nil service did not resolve to the defaults: %+v", cfg)
		}
	})
}

// Preview must go through the same resolver as a real render, or the customer
// approves one thing and their board receives another.
func TestPreviewUsesTheSubmittedRecordNotTheStoredOne(t *testing.T) {
	svc := NewService(&fakeRepo{rec: &domain.ReportBranding{PrimaryColor: "#1C3A62"}},
		newFakeStore(), &fakeCompanies{company: &domain.Company{Name: "Contoh"}})

	cfg := svc.Preview(context.Background(), "co-1",
		&domain.ReportBranding{PrimaryColor: "#713F1C", LegalName: "Unsaved Name"}, nil)

	if cfg.Primary.Hex() != "#713F1C" {
		t.Errorf("preview used %s, want the submitted colour", cfg.Primary.Hex())
	}
	if cfg.Name != "Unsaved Name" {
		t.Errorf("preview name = %q, want the submitted one", cfg.Name)
	}
}
