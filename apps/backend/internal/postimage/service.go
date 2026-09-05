// Package postimage owns the pictures a tenant supplies for the agent to draw
// into a post (T-G12): storing them, listing them, and resolving the name a
// model wrote to the bytes a renderer needs.
//
// It is its own package for `branding`'s reason, and the reason has the same
// shape: two callers must not diverge. The HTTP handler that uploads and the
// `generate_document` door that resolves both have to agree about what a
// stored image is — normalised to PNG, bounded, addressed by a name unique
// within one company — and a second answer in the tool is how "it looked
// right in the library and wrong on the card" happens.
package postimage

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder for image.Decode
	"image/png"
	"io"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/image/draw"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ObjectStore is the slice of the storage adapter this package needs,
// declared at the consumer so the service is testable without MinIO.
type ObjectStore interface {
	UploadKey(ctx context.Context, key string, r io.Reader, contentType string) (string, error)
	DownloadKey(ctx context.Context, key string) ([]byte, error)
	RemoveKey(ctx context.Context, key string) error
}

// Limits on an uploaded picture.
const (
	// MaxBytes is what the endpoint accepts. Eight times the logo's cap,
	// because this is a photograph and that is a mark — a phone camera's JPEG
	// of a product on a table is routinely two megabytes, and refusing it
	// would send every shop owner to an image compressor before they could use
	// the feature.
	MaxBytes = 4 << 20

	// MaxEdge is the long edge after re-encoding. The widest surface a card is
	// drawn on is 1920px and the photograph never fills it, so 2048 is already
	// past what any output can use. Anything larger is downscaled rather than
	// refused, for `branding.MaxLogoEdge`'s reason: "your file is too big" is a
	// worse answer than a correctly sized picture.
	MaxEdge = 2048
)

// Service stores and resolves a company's picture library.
type Service struct {
	repo  domain.PostImageRepository
	store ObjectStore
	// newID is the id generator, injectable so a test can pin the key.
	newID func() string
}

func NewService(repo domain.PostImageRepository, store ObjectStore) *Service {
	return &Service{repo: repo, store: store, newID: uuid.NewString}
}

// Available reports whether this deployment can store pictures at all. A
// deployment with no object storage has no library, and the tool's own
// description says so rather than offering a slot that cannot be filled.
func (s *Service) Available() bool {
	return s != nil && s.store != nil && s.repo != nil
}

// Upload normalises the picture, stores it, and writes the row.
//
// **The object is written first, and removed if the row fails.** The other
// order leaves a row pointing at nothing: a picture that appears in the
// library, is offered to the model, resolves, and then renders a card with a
// hole in it. An orphaned object is invisible and costs storage; an orphaned
// row is a defect somebody has to reproduce.
func (s *Service) Upload(ctx context.Context, companyID, userID, name, alt string, r io.Reader) (*domain.PostImage, error) {
	if !s.Available() {
		return nil, fmt.Errorf("%w: image storage is not configured on this deployment", domain.ErrInvalidInput)
	}
	img := &domain.PostImage{
		CompanyID:  companyID,
		Name:       strings.TrimSpace(name),
		Alt:        strings.TrimSpace(alt),
		UploadedBy: userID,
	}
	if err := img.Validate(); err != nil {
		return nil, err
	}

	// One byte past the limit is enough to know it is over it, so a 50 MB
	// upload costs MaxBytes of memory rather than 50.
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: could not read the upload: %s", domain.ErrInvalidInput, err.Error())
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: the upload is empty", domain.ErrInvalidInput)
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("%w: the image must be %d MB or smaller", domain.ErrInvalidInput, MaxBytes>>20)
	}

	encoded, w, h, err := Normalize(raw)
	if err != nil {
		return nil, err
	}
	img.Width, img.Height, img.ByteSize = w, h, int64(len(encoded))

	// The key contains the id, so the id is generated here rather than by the
	// database's DEFAULT: the object has to be written before the row, and it
	// cannot be addressed until something has named it.
	img.ID = s.newID()
	img.StorageKey = domain.PostImageKey(companyID, img.ID)
	if _, err := s.store.UploadKey(ctx, img.StorageKey, bytes.NewReader(encoded), "image/png"); err != nil {
		return nil, fmt.Errorf("store image: %w", err)
	}
	if err := s.repo.Insert(ctx, img); err != nil {
		// Detached from the request's context: the row failed, and the cleanup
		// has to happen because of that rather than only while the caller is
		// still waiting.
		if rmErr := s.store.RemoveKey(context.WithoutCancel(ctx), img.StorageKey); rmErr != nil {
			return nil, fmt.Errorf("%w (its file was left behind: %s)", err, rmErr.Error())
		}
		return nil, err
	}
	return img, nil
}

// List is the tenant's library, newest first.
func (s *Service) List(ctx context.Context, companyID string) ([]*domain.PostImage, error) {
	if !s.Available() {
		return []*domain.PostImage{}, nil
	}
	return s.repo.ListByCompany(ctx, companyID)
}

// Get reads one row, company-scoped.
func (s *Service) Get(ctx context.Context, companyID, id string) (*domain.PostImage, error) {
	if !s.Available() {
		return nil, domain.ErrNotFound
	}
	return s.repo.GetForCompany(ctx, companyID, id)
}

// Bytes reads one image's PNG. Company-scoped by the row it is found through,
// so another tenant's id is a not-found rather than a picture.
func (s *Service) Bytes(ctx context.Context, companyID, id string) (*domain.PostImage, []byte, error) {
	img, err := s.Get(ctx, companyID, id)
	if err != nil {
		return nil, nil, err
	}
	body, err := s.store.DownloadKey(ctx, img.StorageKey)
	if err != nil {
		return nil, nil, fmt.Errorf("read image %s: %w", id, err)
	}
	return img, body, nil
}

// Resolve turns what a model wrote into one image.
//
// The id first and the name second, because a spec can carry either: a model
// repeating an id it saw earlier in the conversation, and a model naming what
// it wants. **A miss is nil and not an error** — a promotion whose photograph
// could not be found still renders as a card, and the caller's job is to say
// so rather than to fail a turn over a picture.
func (s *Service) Resolve(ctx context.Context, companyID, ref string) *domain.PostImage {
	ref = strings.TrimSpace(ref)
	if !s.Available() || ref == "" {
		return nil
	}
	if img, err := s.repo.GetForCompany(ctx, companyID, ref); err == nil && img != nil {
		return img
	}
	img, err := s.repo.FindByName(ctx, companyID, ref)
	if err != nil {
		return nil
	}
	return img
}

// Delete removes the row and then the object. Row first here and object first
// in Upload, and both orders serve one rule: never leave something that is
// referenced and cannot be read.
func (s *Service) Delete(ctx context.Context, companyID, id string) error {
	if !s.Available() {
		return domain.ErrNotFound
	}
	img, err := s.repo.GetForCompany(ctx, companyID, id)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, companyID, id); err != nil {
		return err
	}
	if err := s.store.RemoveKey(ctx, img.StorageKey); err != nil {
		return fmt.Errorf("the image was removed from the library but its file could not be deleted: %w", err)
	}
	return nil
}

// Normalize decodes, downscales if needed, and re-encodes as PNG. It returns
// the bytes and the dimensions that are stored with the row.
//
// PNG for the reason the logo is PNG: one format downstream means the plan's
// data URI has one media type and the renderer has one decoder. The cost is
// file size on a photograph, which is what MaxEdge bounds.
func Normalize(raw []byte) (encoded []byte, width, height int, err error) {
	decoded, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		// image.Decode's own error tells a shop owner nothing about what they
		// should have uploaded instead.
		return nil, 0, 0, fmt.Errorf("%w: the image must be a PNG or JPEG", domain.ErrInvalidInput)
	}
	switch format {
	case "png", "jpeg":
	default:
		// Unreachable today — png and jpeg are the only registered decoders —
		// and here so that a decoder registered elsewhere in the binary cannot
		// widen what this endpoint accepts. SVG in particular is a script
		// injection surface in a renderer that is a browser.
		return nil, 0, 0, fmt.Errorf("%w: the image must be a PNG or JPEG, got %s", domain.ErrInvalidInput, format)
	}

	b := decoded.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, 0, 0, fmt.Errorf("%w: the image has no pixels", domain.ErrInvalidInput)
	}
	if b.Dx() > MaxEdge || b.Dy() > MaxEdge {
		decoded = downscale(decoded, MaxEdge)
		b = decoded.Bounds()
	}

	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&out, decoded); err != nil {
		return nil, 0, 0, fmt.Errorf("re-encode image: %w", err)
	}
	return out.Bytes(), b.Dx(), b.Dy(), nil
}

// downscale fits the image inside a max×max box, keeping its aspect ratio.
// CatmullRom for `branding.downscale`'s reason: the cheap kernels destroy
// edges, and a product photograph is mostly edges once it is this small.
func downscale(src image.Image, max int) image.Image {
	b := src.Bounds()
	scale := float64(max) / float64(b.Dx())
	if b.Dy() > b.Dx() {
		scale = float64(max) / float64(b.Dy())
	}
	w := int(float64(b.Dx()) * scale)
	h := int(float64(b.Dy()) * scale)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
