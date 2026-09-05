package docgen

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/video"
	"github.com/fauzanebd/argentum/internal/report/videoplan"
)

// stillsService fakes apps/render's stills mode (T-G5) for the docgen path
// (T-G6): every submitted plan comes back as one JPEG-shaped page per scene,
// numbered, so the test can tell page 3 from page 2 in the bucket.
func stillsService(t *testing.T) *httptest.Server {
	t.Helper()
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/render":
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte(`"output":"stills"`)) || !bytes.Contains(body, []byte(`"still":true`)) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"not a stills request for a still plan"}`))
				return
			}
			pages = bytes.Count(body, []byte(`"kind":`))
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"44444444-4444-4444-4444-444444444444"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/44444444-4444-4444-4444-444444444444":
			fmt.Fprintf(w, `{"state":"done","progress":1,"pages":%d,"frames":%d,"render_seconds":4.2}`, pages, pages)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/44444444-4444-4444-4444-444444444444/result/"):
			var page int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/v1/jobs/44444444-4444-4444-4444-444444444444/result/"), "%d", &page)
			w.Header().Set("Content-Type", "image/jpeg")
			fmt.Fprintf(w, "JPEG:%d", page)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func carouselSpec() *spec.Document {
	return &spec.Document{
		SpecVersion: 2,
		Format:      "carousel",
		Title:       "Penjualan Agustus",
		Locale:      "id",
		Currency:    "IDR",
		GeneratedAt: "2026-09-01T02:00:00Z",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionCover, Text: "Penjualan Agustus", Period: "AGUSTUS 2026"},
			{Type: spec.SectionHeading, Level: 1, Text: "Sorotan"},
			{Type: spec.SectionKPIRow, Items: []spec.Item{
				{Label: "Total Pendapatan", Value: &spec.Cell{V: 412350000, Fmt: "currency"}},
				{Label: "Jumlah Pesanan", Value: &spec.Cell{V: 5124, Fmt: "number"}},
			}},
			{Type: spec.SectionParagraph, Text: "Pendapatan naik 9,8% dibanding Juli, didorong kanal WhatsApp."},
		}},
		Social: &spec.Social{Caption: "Agustus tumbuh 9,8% 🎉", Hashtags: []string{"promo", "#agustus"}},
	}
}

func carouselService(t *testing.T, store *fakeStore, docs *fakeDocs) *Service {
	t.Helper()
	srv := stillsService(t)
	client := video.New(video.Options{BaseURL: srv.URL, Timeout: 5 * time.Second, PollEvery: time.Millisecond})
	return newTestService(store, docs, &countingMeter{}).WithVideo(client, videoplan.Limits{})
}

// TestACarouselIsPagesBesideAZip is the whole storage contract: the zip at the
// document's key, one page a slide under its prefix, a manifest beside them,
// the count on the row, and the manifest handed back for the announcement.
func TestACarouselIsPagesBesideAZip(t *testing.T) {
	store, docs := newFakeStore(), &fakeDocs{}
	svc := carouselService(t, store, docs)

	res, err := svc.Generate(context.Background(), Input{Spec: carouselSpec(), CompanyID: "co-1", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	doc := res.Document
	// cover, divider, kpi, statement, closing
	if doc.PageCount != 5 {
		t.Fatalf("page_count = %d, want 5", doc.PageCount)
	}
	if doc.Format != domain.DocumentFormatCarousel || !strings.HasSuffix(doc.StorageKey, ".zip") {
		t.Errorf("document is %s at %s", doc.Format, doc.StorageKey)
	}
	if len(docs.rows) != 1 || docs.rows[0].PageCount != 5 {
		t.Errorf("the persisted row does not carry the page count: %+v", docs.rows)
	}

	// The pages, under the document's prefix, in order.
	for i := 1; i <= 5; i++ {
		key := PageKey(doc.StorageKey, i)
		if !strings.HasPrefix(key, strings.TrimSuffix(doc.StorageKey, ".zip")+"/") {
			t.Errorf("page key %q is not under the document's prefix", key)
		}
		if got := string(store.objects[key]); got != fmt.Sprintf("JPEG:%d", i) {
			t.Errorf("page %d at %s = %q", i, key, got)
		}
	}
	if _, ok := store.objects[ManifestKey(doc.StorageKey)]; !ok {
		t.Error("no manifest stored beside the pages")
	}
	if _, ok := store.objects[PageKey(doc.StorageKey, 6)]; ok {
		t.Error("a sixth page exists")
	}

	// The manifest the announcement is written from.
	if res.Carousel == nil {
		t.Fatal("Result.Carousel is nil for a carousel")
	}
	if res.Carousel.Caption != "Agustus tumbuh 9,8% 🎉" || strings.Join(res.Carousel.Hashtags, ",") != "promo,agustus" {
		t.Errorf("manifest text = %q %v", res.Carousel.Caption, res.Carousel.Hashtags)
	}
	if len(res.Carousel.Alts) != 5 || res.Carousel.Alts[2] == "" {
		t.Errorf("alts = %v, want five non-empty", res.Carousel.Alts)
	}
	if !strings.Contains(res.Carousel.Alts[2], "Total Pendapatan") {
		t.Errorf("the KPI slide's alt does not name its card: %q", res.Carousel.Alts[2])
	}

	// The zip: the pages, the caption as pasteable text, the manifest.
	zr, err := zip.NewReader(bytes.NewReader(res.Data), int64(len(res.Data)))
	if err != nil {
		t.Fatalf("the document is not a zip: %v", err)
	}
	names := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(body)
	}
	for _, want := range []string{"01.jpg", "05.jpg", "caption.txt", "carousel.json"} {
		if _, ok := names[want]; !ok {
			t.Errorf("zip lacks %s; has %v", want, keys(names))
		}
	}
	if names["caption.txt"] != "Agustus tumbuh 9,8% 🎉\n\n#promo #agustus" {
		t.Errorf("caption.txt = %q", names["caption.txt"])
	}
	if names["03.jpg"] != "JPEG:3" {
		t.Errorf("03.jpg = %q", names["03.jpg"])
	}
}

// LoadPage answers the route: the page's bytes inside the count, not-found
// outside it or for a document that has no pages.
func TestLoadPageIsBoundedByTheRow(t *testing.T) {
	store, docs := newFakeStore(), &fakeDocs{}
	svc := carouselService(t, store, docs)
	res, err := svc.Generate(context.Background(), Input{Spec: carouselSpec(), CompanyID: "co-1", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	body, err := svc.LoadPage(context.Background(), res.Document, 2)
	if err != nil || string(body) != "JPEG:2" {
		t.Errorf("LoadPage(2) = %q, %v", body, err)
	}
	for _, page := range []int{0, 6, -1} {
		if _, err := svc.LoadPage(context.Background(), res.Document, page); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("LoadPage(%d) = %v, want ErrNotFound", page, err)
		}
	}
	pdf := &domain.Document{Format: domain.DocumentFormatPDF, StorageKey: "documents/co-1/th-1/x.pdf", PageCount: 0}
	if _, err := svc.LoadPage(context.Background(), pdf, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LoadPage on a pdf = %v, want ErrNotFound", err)
	}
	// A row claiming pages the bucket does not hold is a read error, not a
	// not-found: the row is the authority on the count, and a missing object
	// under it is something to alert on.
	lying := *res.Document
	lying.PageCount = 9
	if _, err := svc.LoadPage(context.Background(), &lying, 9); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a missing page object answered %v, want a read error", err)
	}
}

// The slide band is checked at the door: CheckVideoLimits routes a carousel to
// the carousel's caps, and an eleven-beat spec is refused with the sentence.
func TestCheckVideoLimitsRoutesACarouselToTheSlideBand(t *testing.T) {
	svc := newTestService(newFakeStore(), &fakeDocs{}, nil).WithVideo(nil, videoplan.Limits{})
	long := carouselSpec()
	for i := 0; i < 8; i++ {
		long.Content.Sections = append(long.Content.Sections,
			spec.Section{Type: spec.SectionHeading, Level: 1, Text: fmt.Sprintf("Bagian %d", i)})
	}
	err := svc.CheckVideoLimits(long)
	if err == nil || !strings.Contains(err.Error(), "a carousel is 2–10 slides") {
		t.Fatalf("CheckVideoLimits = %v, want the slide-band refusal", err)
	}
	if err := svc.CheckVideoLimits(carouselSpec()); err != nil {
		t.Errorf("a five-slide carousel was refused: %v", err)
	}
	// A pdf has no slide band and no scene cap.
	pdf := carouselSpec()
	pdf.Format = "pdf"
	if err := svc.CheckVideoLimits(pdf); err != nil {
		t.Errorf("a pdf was checked against a render cap: %v", err)
	}
}

// Without a render service the format is unavailable, not broken: the same
// sentinel the video answers.
func TestACarouselWithoutARenderServiceIsNotConfigured(t *testing.T) {
	svc := newTestService(newFakeStore(), &fakeDocs{}, nil)
	_, err := svc.Generate(context.Background(), Input{Spec: carouselSpec(), CompanyID: "co-1", ThreadID: "th-1"})
	if !errors.Is(err, video.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestCaptionTextWritesTheHashOnce(t *testing.T) {
	got := CaptionText(&CarouselManifest{Caption: " Halo ", Hashtags: []string{"#a", "b", "", "#"}})
	if got != "Halo\n\n#a #b" {
		t.Errorf("CaptionText = %q", got)
	}
	if CaptionText(nil) != "" || CaptionText(&CarouselManifest{}) != "" {
		t.Error("an empty manifest produced caption text")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// PresignPage is the channel's way to a slide (T-G6, finding 6): a signed URL
// per page on the same TTL as the document's own link, bounded by the row
// exactly as LoadPage is.
func TestPresignPageIsBoundedByTheRow(t *testing.T) {
	store, docs := newFakeStore(), &fakeDocs{}
	svc := carouselService(t, store, docs)
	res, err := svc.Generate(context.Background(), Input{Spec: carouselSpec(), CompanyID: "co-1", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	u, err := svc.PresignPage(context.Background(), res.Document, 2)
	if err != nil {
		t.Fatalf("PresignPage(2): %v", err)
	}
	if !strings.Contains(u, PageKey(res.Document.StorageKey, 2)) {
		t.Errorf("PresignPage(2) = %q, want the page's own key signed", u)
	}
	if !strings.Contains(u, "exp="+svc.PresignTTL().String()) {
		t.Errorf("PresignPage(2) = %q, want the document's TTL", u)
	}
	for _, page := range []int{0, 6, -1} {
		if _, err := svc.PresignPage(context.Background(), res.Document, page); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("PresignPage(%d) = %v, want ErrNotFound", page, err)
		}
	}
	pdf := &domain.Document{Format: domain.DocumentFormatPDF, StorageKey: "documents/co-1/th-1/x.pdf"}
	if _, err := svc.PresignPage(context.Background(), pdf, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("PresignPage on a pdf = %v, want ErrNotFound", err)
	}
}
