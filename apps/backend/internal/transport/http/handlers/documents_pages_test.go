package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The page route (T-G6): a slide to the session that owns it, and a not-found
// for everything else — another tenant's id, a page past the count, a
// document that has no pages.

type pageDocs struct{ rows map[string]*domain.Document }

func (d *pageDocs) Insert(context.Context, *domain.Document) error { return nil }
func (d *pageDocs) GetByID(_ context.Context, id string) (*domain.Document, error) {
	if r, ok := d.rows[id]; ok {
		return r, nil
	}
	return nil, domain.ErrNotFound
}
func (d *pageDocs) GetForCompany(_ context.Context, companyID, id string) (*domain.Document, error) {
	if r, ok := d.rows[id]; ok && r.CompanyID == companyID {
		return r, nil
	}
	return nil, domain.ErrNotFound
}
func (d *pageDocs) ListByCompany(context.Context, string, domain.DocumentFilter) ([]*domain.Document, bool, error) {
	return nil, false, nil
}
func (d *pageDocs) ListByThread(context.Context, string) ([]*domain.Document, error) { return nil, nil }

type pageStore struct{ objects map[string][]byte }

func (s *pageStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	b, _ := io.ReadAll(r)
	s.objects[key] = b
	return key, nil
}
func (s *pageStore) PresignKey(_ context.Context, key string, _ time.Duration) (string, error) {
	return "http://store.invalid/" + key, nil
}
func (s *pageStore) DownloadKey(_ context.Context, key string) ([]byte, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return b, nil
}

func pagesRouter(t *testing.T, company string) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := &pageStore{objects: map[string][]byte{}}
	key := "documents/co-1/th-1/doc-1.zip"
	for i := 1; i <= 3; i++ {
		store.objects[docgen.PageKey(key, i)] = []byte("JPEG:" + string(rune('0'+i)))
	}
	docs := &pageDocs{rows: map[string]*domain.Document{
		"doc-1": {ID: "doc-1", CompanyID: "co-1", Format: domain.DocumentFormatCarousel, StorageKey: key, PageCount: 3},
		"doc-2": {ID: "doc-2", CompanyID: "co-1", Format: domain.DocumentFormatPDF, StorageKey: "documents/co-1/th-1/doc-2.pdf"},
	}}
	gen := docgen.New(store, docs, nil, nil, nil, time.Hour)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("company_id", company) })
	NewDocumentsHandler(docs, gen).Register(r.Group("/api"))
	return r
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestAPageIsServedToItsOwnerAsJPEG(t *testing.T) {
	w := get(pagesRouter(t, "co-1"), "/api/documents/doc-1/pages/2")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content-type %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("cache-control %q — a shared cache must not serve one tenant's slide to the next", cc)
	}
	if w.Body.String() != "JPEG:2" {
		t.Errorf("body %q", w.Body.String())
	}
}

func TestPagesOutsideTheCountAreNotFound(t *testing.T) {
	h := pagesRouter(t, "co-1")
	for _, path := range []string{
		"/api/documents/doc-1/pages/0",
		"/api/documents/doc-1/pages/4",
		"/api/documents/doc-1/pages/x",
		"/api/documents/doc-2/pages/1", // a pdf has no pages
		"/api/documents/doc-9/pages/1", // no such document
	} {
		if w := get(h, path); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
	}
}

// The tenant boundary is the query: another company's session gets the same
// not-found a missing document gets, never a 403 that confirms the id exists.
func TestAnotherTenantsPageIsNotFound(t *testing.T) {
	if w := get(pagesRouter(t, "co-2"), "/api/documents/doc-1/pages/1"); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant page: status %d, want 404", w.Code)
	}
}
