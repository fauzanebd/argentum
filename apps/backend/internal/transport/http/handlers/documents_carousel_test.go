package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The carousel route (T-G7): the words beside the slides.
//
// The pages have been readable since T-G6 and the caption has not, which is
// why an approval card could show a post without showing the text of it. These
// reuse documents_pages_test.go's fixtures — same fake store, same two
// documents — and add the manifest that route never needed.

// manifestRouter builds the same two documents pagesRouter does — a carousel
// and a pdf — and writes a manifest beside the carousel's pages. It keeps its
// own store rather than reaching into pagesRouter's, so neither test can move
// the other's fixtures out from under it.
func manifestRouter(t *testing.T, company string, m docgen.CarouselManifest) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	key := "documents/co-1/th-1/doc-1.zip"
	store := &pageStore{objects: map[string][]byte{}}
	for i := 1; i <= 3; i++ {
		store.objects[docgen.PageKey(key, i)] = []byte("JPEG")
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	store.objects[docgen.ManifestKey(key)] = body

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

func TestTheCaptionIsServedWithItsSlideCount(t *testing.T) {
	h := manifestRouter(t, "co-1", docgen.CarouselManifest{
		Caption:  "Diskon akhir pekan",
		Hashtags: []string{"promo", "#gelael"},
		Alts:     []string{"Sampul", "Harga", "Penutup"},
		Pages:    3,
	})
	w := get(h, "/api/documents/doc-1/carousel")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("cache-control %q — a caption is the tenant's copy", cc)
	}
	var got carouselResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The assembled string, not the parts: this is what "Copy caption" copies,
	// and a hash already carrying its "#" must not come back doubled.
	want := "Diskon akhir pekan\n\n#promo #gelael"
	if got.Caption != want {
		t.Errorf("caption = %q, want %q", got.Caption, want)
	}
	if got.Pages != 3 || len(got.Alts) != 3 {
		t.Errorf("pages = %d, alts = %d, want 3 and 3", got.Pages, len(got.Alts))
	}
}

// A document that is not a carousel is a not-found rather than an empty
// manifest, which is what lets the approval card decide "this proposal is
// about a post" from one request instead of two.
func TestOnlyACarouselHasAManifest(t *testing.T) {
	h := manifestRouter(t, "co-1", docgen.CarouselManifest{Caption: "x", Pages: 3})
	for _, path := range []string{
		"/api/documents/doc-2/carousel", // a pdf
		"/api/documents/doc-9/carousel", // no such document
	} {
		if w := get(h, path); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
	}
}

// The tenant boundary is the query, exactly as it is for a page.
func TestAnotherTenantsCaptionIsNotFound(t *testing.T) {
	h := manifestRouter(t, "co-2", docgen.CarouselManifest{Caption: "x", Pages: 3})
	if w := get(h, "/api/documents/doc-1/carousel"); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant manifest: status %d, want 404", w.Code)
	}
}
