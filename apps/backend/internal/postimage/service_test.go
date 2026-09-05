package postimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// --- fakes ---

type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	failPut bool
	removed []string
}

func newFakeStore() *fakeStore { return &fakeStore{objects: map[string][]byte{}} }

func (s *fakeStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	if s.failPut {
		return "", errors.New("bucket is on fire")
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return key, nil
}

func (s *fakeStore) DownloadKey(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return b, nil
}

func (s *fakeStore) RemoveKey(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, key)
	delete(s.objects, key)
	return nil
}

type fakeRepo struct {
	rows      []*domain.PostImage
	failWrite bool
}

func (r *fakeRepo) Insert(_ context.Context, img *domain.PostImage) error {
	if r.failWrite {
		return errors.New("the database said no")
	}
	for _, existing := range r.rows {
		if existing.CompanyID == img.CompanyID && strings.EqualFold(existing.Name, img.Name) {
			return domain.ErrAlreadyExists
		}
	}
	cp := *img
	r.rows = append(r.rows, &cp)
	return nil
}

func (r *fakeRepo) GetForCompany(_ context.Context, companyID, id string) (*domain.PostImage, error) {
	for _, row := range r.rows {
		if row.ID == id && row.CompanyID == companyID {
			cp := *row
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.PostImage, error) {
	out := []*domain.PostImage{}
	for _, row := range r.rows {
		if row.CompanyID == companyID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *fakeRepo) FindByName(_ context.Context, companyID, name string) (*domain.PostImage, error) {
	for _, row := range r.rows {
		if row.CompanyID == companyID && strings.EqualFold(row.Name, name) {
			cp := *row
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeRepo) Delete(_ context.Context, companyID, id string) error {
	for i, row := range r.rows {
		if row.ID == id && row.CompanyID == companyID {
			r.rows = append(r.rows[:i], r.rows[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// --- helpers ---

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: 120, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestService(repo *fakeRepo, store *fakeStore) *Service {
	s := NewService(repo, store)
	n := 0
	s.newID = func() string { n++; return "img-" + string(rune('0'+n)) }
	return s
}

// --- tests ---

func TestUploadStoresTheObjectAndTheRow(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)

	img, err := svc.Upload(context.Background(), "co-1", "user-1", "Jeruk Cara Cara", "Jeruk dibelah", bytes.NewReader(pngBytes(t, 400, 300)))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if img.Width != 400 || img.Height != 300 {
		t.Errorf("dimensions = %dx%d, want 400x300", img.Width, img.Height)
	}
	if got := img.Aspect(); got < 1.33 || got > 1.34 {
		t.Errorf("aspect = %v, want ~1.333", got)
	}
	if _, ok := store.objects[img.StorageKey]; !ok {
		t.Errorf("no object at %q", img.StorageKey)
	}
	if img.StorageKey != domain.PostImageKey("co-1", img.ID) {
		t.Errorf("key = %q, want the company-scoped one", img.StorageKey)
	}
	if len(repo.rows) != 1 {
		t.Errorf("%d rows written", len(repo.rows))
	}
}

// A JPEG from a phone is what a shop owner actually has. It is accepted and
// re-encoded, so everything downstream sees exactly one format.
func TestAJPEGIsAcceptedAndStoredAsPNG(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)

	img, err := svc.Upload(context.Background(), "co-1", "", "Kopi", "", bytes.NewReader(jpegBytes(t, 200, 200)))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	stored := store.objects[img.StorageKey]
	if _, format, err := image.Decode(bytes.NewReader(stored)); err != nil || format != "png" {
		t.Errorf("stored format = %q (%v), want png", format, err)
	}
}

// An oversized picture is resized rather than refused: "your file is too big"
// is a worse answer than a correctly sized one.
func TestAnOversizedImageIsDownscaledNotRefused(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)

	img, err := svc.Upload(context.Background(), "co-1", "", "Besar", "", bytes.NewReader(pngBytes(t, MaxEdge+600, 1000)))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if img.Width != MaxEdge {
		t.Errorf("width = %d, want the long edge capped at %d", img.Width, MaxEdge)
	}
	if img.Height >= 1000 {
		t.Errorf("height = %d; the aspect ratio was not kept", img.Height)
	}
}

// The one thing this package must never leave behind: a row pointing at an
// object that is not there. The row write fails, so the object goes too.
func TestAFailedRowRemovesTheObject(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{failWrite: true}
	svc := newTestService(repo, store)

	if _, err := svc.Upload(context.Background(), "co-1", "", "Jeruk", "", bytes.NewReader(pngBytes(t, 100, 100))); err == nil {
		t.Fatal("a failed row write reported success")
	}
	if len(store.objects) != 0 {
		t.Errorf("%d objects left behind: %v", len(store.objects), store.removed)
	}
	if len(store.removed) != 1 {
		t.Errorf("the object was not removed: %v", store.removed)
	}
}

// The name is the model's handle, so two pictures cannot share one.
func TestADuplicateNameIsRefused(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)
	ctx := context.Background()

	if _, err := svc.Upload(ctx, "co-1", "", "Jeruk", "", bytes.NewReader(pngBytes(t, 50, 50))); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Upload(ctx, "co-1", "", "jeruk", "", bytes.NewReader(pngBytes(t, 50, 50)))
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("second upload of the same name = %v, want ErrAlreadyExists", err)
	}
	// And another company may use it, because the constraint is per tenant.
	if _, err := svc.Upload(ctx, "co-2", "", "Jeruk", "", bytes.NewReader(pngBytes(t, 50, 50))); err != nil {
		t.Errorf("another company was refused the same name: %v", err)
	}
}

func TestNonImagesAreRefusedWithAnActionableSentence(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)

	_, err := svc.Upload(context.Background(), "co-1", "", "Bukan gambar", "", strings.NewReader("%PDF-1.7 not an image"))
	if err == nil {
		t.Fatal("a PDF was accepted as a picture")
	}
	if !errors.Is(err, domain.ErrInvalidInput) || !strings.Contains(err.Error(), "PNG or JPEG") {
		t.Errorf("refusal = %v, want one naming the formats", err)
	}
}

// Resolve is what the tool calls with whatever the model wrote.
func TestResolveTakesAnIDOrAName(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)
	ctx := context.Background()
	img, err := svc.Upload(ctx, "co-1", "", "Jeruk Cara Cara", "", bytes.NewReader(pngBytes(t, 60, 60)))
	if err != nil {
		t.Fatal(err)
	}

	if got := svc.Resolve(ctx, "co-1", img.ID); got == nil || got.ID != img.ID {
		t.Errorf("by id = %v", got)
	}
	if got := svc.Resolve(ctx, "co-1", "jeruk cara cara"); got == nil || got.ID != img.ID {
		t.Errorf("by lower-cased name = %v", got)
	}
	if got := svc.Resolve(ctx, "co-1", "  Jeruk Cara Cara  "); got == nil {
		t.Error("a name with whitespace did not resolve")
	}
	// A miss is nil and not an error: the card renders without a photograph.
	if got := svc.Resolve(ctx, "co-1", "durian"); got != nil {
		t.Errorf("an absent name resolved to %v", got)
	}
	// And the tenant boundary holds: another company's id is a miss, not a
	// picture.
	if got := svc.Resolve(ctx, "co-2", img.ID); got != nil {
		t.Errorf("another company resolved our image: %v", got)
	}
	if got := svc.Resolve(ctx, "co-2", "Jeruk Cara Cara"); got != nil {
		t.Errorf("another company resolved our name: %v", got)
	}
}

// Bytes is company-scoped through the row, so an id alone is not a credential.
func TestBytesIsCompanyScoped(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)
	ctx := context.Background()
	img, _ := svc.Upload(ctx, "co-1", "", "Jeruk", "", bytes.NewReader(pngBytes(t, 60, 60)))

	if _, body, err := svc.Bytes(ctx, "co-1", img.ID); err != nil || len(body) == 0 {
		t.Errorf("own image: %d bytes, %v", len(body), err)
	}
	if _, _, err := svc.Bytes(ctx, "co-2", img.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another company read the image: %v", err)
	}
}

func TestDeleteRemovesRowAndObject(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)
	ctx := context.Background()
	img, _ := svc.Upload(ctx, "co-1", "", "Jeruk", "", bytes.NewReader(pngBytes(t, 60, 60)))

	if err := svc.Delete(ctx, "co-2", img.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another company deleted our image: %v", err)
	}
	if err := svc.Delete(ctx, "co-1", img.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(repo.rows) != 0 || len(store.objects) != 0 {
		t.Errorf("%d rows and %d objects left", len(repo.rows), len(store.objects))
	}
}

// A deployment with no object storage has no library, and every read answers
// as though it were empty rather than panicking on a nil store.
func TestNoStorageMeansNoLibrary(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	ctx := context.Background()

	if svc.Available() {
		t.Error("a service with no store reports available")
	}
	if imgs, err := svc.List(ctx, "co-1"); err != nil || len(imgs) != 0 {
		t.Errorf("List = %v, %v", imgs, err)
	}
	if got := svc.Resolve(ctx, "co-1", "jeruk"); got != nil {
		t.Errorf("Resolve = %v", got)
	}
	if _, err := svc.Upload(ctx, "co-1", "", "Jeruk", "", bytes.NewReader(pngBytes(t, 10, 10))); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("Upload = %v, want a refusal naming the deployment", err)
	}
}

// The bounds are refusals rather than truncations: a silently shortened name
// is a picture the model can no longer find.
func TestBoundsAreRefusedNotTruncated(t *testing.T) {
	store, repo := newFakeStore(), &fakeRepo{}
	svc := newTestService(repo, store)

	long := strings.Repeat("a", domain.MaxPostImageNameChars+1)
	if _, err := svc.Upload(context.Background(), "co-1", "", long, "", bytes.NewReader(pngBytes(t, 10, 10))); err == nil {
		t.Error("an over-long name was accepted")
	}
	if _, err := svc.Upload(context.Background(), "co-1", "", "", "", bytes.NewReader(pngBytes(t, 10, 10))); err == nil {
		t.Error("an unnamed image was accepted")
	}
	if len(store.objects) != 0 {
		t.Error("a refused upload still wrote an object")
	}
}
