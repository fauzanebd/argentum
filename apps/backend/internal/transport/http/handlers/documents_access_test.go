package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z11: a generated document is as restricted as the conversation that made
// it. Which conversations a person may read is T-Z10's rule, decided and tested
// in internal/app; this is that every dashboard route listing or opening a
// generated document asks — once per page — and answers a hidden one exactly as
// a missing one.

// fourDocuments is one company's generated documents: one from a readable
// conversation, two from a hidden one, and one with no conversation at all.
type fourDocuments struct{ pageDocs }

func newFourDocuments() *fourDocuments {
	return &fourDocuments{pageDocs{rows: map[string]*domain.Document{
		"doc-open":     {ID: "doc-open", CompanyID: "co-1", ThreadID: "th-open", Format: domain.DocumentFormatPDF, Filename: "weekly-sales.pdf"},
		"doc-hidden":   {ID: "doc-hidden", CompanyID: "co-1", ThreadID: "th-hidden", Format: domain.DocumentFormatCarousel, Filename: "payroll-march.zip", PageCount: 3},
		"doc-hidden-2": {ID: "doc-hidden-2", CompanyID: "co-1", ThreadID: "th-hidden", Format: domain.DocumentFormatPDF, Filename: "payroll-april.pdf"},
		"doc-render":   {ID: "doc-render", CompanyID: "co-1", Format: domain.DocumentFormatPDF, Filename: "rendered.pdf"},
	}}}
}

// ListByCompany answers in a fixed order, newest first as the repository does.
func (d *fourDocuments) ListByCompany(context.Context, string, domain.DocumentFilter) ([]*domain.Document, bool, error) {
	var out []*domain.Document
	for _, id := range []string{"doc-hidden", "doc-open", "doc-hidden-2", "doc-render"} {
		out = append(out, d.rows[id])
	}
	return out, false, nil
}

// countingReader is hiddenConversations with a record of what it was asked.
type countingReader struct {
	hiddenConversations
	readable [][]string
	mayRead  []string
}

func (r *countingReader) Readable(ctx context.Context, companyID, userID string, ids []string) ([]string, error) {
	r.readable = append(r.readable, slices.Clone(ids))
	return r.hiddenConversations.Readable(ctx, companyID, userID, ids)
}

func (r *countingReader) MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error) {
	r.mayRead = append(r.mayRead, threadID)
	out, err := r.hiddenConversations.Readable(ctx, companyID, userID, []string{threadID})
	return len(out) == 1, err
}

// linkedShares holds one live link each on doc-open and doc-hidden. It embeds
// the interface, so a route reaching Insert — minting — panics, which is the
// signal that a refused mint got further than it should have.
type linkedShares struct {
	domain.ReportShareRepository
	revoked []string
}

func (s *linkedShares) ListForDocument(_ context.Context, _, documentID string) ([]*domain.ReportShare, error) {
	if documentID == "doc-open" || documentID == "doc-hidden" {
		return []*domain.ReportShare{{ID: "sh-" + documentID, DocumentID: documentID, ExpiresAt: time.Now().Add(time.Hour)}}, nil
	}
	return nil, nil
}

func (s *linkedShares) Revoke(_ context.Context, _, shareID string) error {
	s.revoked = append(s.revoked, shareID)
	return nil
}

// documentsRouter registers both document handlers as cmd/api does, behind a
// stand-in for Auth. The caller is an admin: nothing on these routes reads the
// role, and decision 4 is that an admin without the grant is hidden from like
// anyone else. The documents handler has no docgen, so a route that gets past
// the check on a document's contents answers 503.
func documentsRouter(t *testing.T, docs domain.DocumentRepository, reader ConversationReader, shares *app.ReportShareService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "admin-1")
		c.Set("role", "admin")
	})
	g := r.Group("/api")
	dh := NewDocumentsHandler(docs, nil)
	sh := NewReportShareHandler(shares)
	if reader != nil {
		dh = dh.WithConversationAccess(reader)
		sh = sh.WithConversationAccess(docs, reader)
	}
	dh.Register(g)
	sh.Register(g)
	return r
}

func listedDocumentIDs(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Documents []struct {
			ID string `json:"id"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	ids := make([]string, 0, len(body.Documents))
	for _, d := range body.Documents {
		ids = append(ids, d.ID)
	}
	return ids
}

func hidingTh() *countingReader {
	return &countingReader{hiddenConversations: hiddenConversations{hidden: map[string]bool{"th-hidden": true}}}
}

func TestTheDocumentListOmitsDocumentsFromHiddenConversations(t *testing.T) {
	reader := hidingTh()
	w := serve(documentsRouter(t, newFourDocuments(), reader, nil), http.MethodGet, "/api/documents")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := listedDocumentIDs(t, w); !slices.Equal(got, []string{"doc-open", "doc-render"}) {
		t.Errorf("listed %v, want the readable conversation's document and the one with no conversation, in order", got)
	}
	if strings.Contains(w.Body.String(), "payroll") {
		t.Errorf("a hidden document's filename is in the list: %s", w.Body.String())
	}
	// One check for the page, each conversation named once — two documents from
	// the same conversation are one question, and a page of fifty is not fifty.
	if len(reader.readable) != 1 || !slices.Equal(reader.readable[0], []string{"th-hidden", "th-open"}) {
		t.Errorf("readability was asked %v, want once, over [th-hidden th-open]", reader.readable)
	}
	if len(reader.mayRead) != 0 {
		t.Errorf("the list asked per document as well: %v", reader.mayRead)
	}
}

func TestWithNothingHiddenEveryDocumentIsListed(t *testing.T) {
	all := []string{"doc-hidden", "doc-open", "doc-hidden-2", "doc-render"}
	for name, reader := range map[string]ConversationReader{
		"not wired":      nil,
		"nothing hidden": hiddenConversations{},
		// What cmd/api hands over when the rule was never built: a typed nil,
		// which is nil-safe and reads everything.
		"a nil rule": (*app.ConversationAccess)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			w := serve(documentsRouter(t, newFourDocuments(), reader, nil), http.MethodGet, "/api/documents")
			if got := listedDocumentIDs(t, w); !slices.Equal(got, all) {
				t.Errorf("listed %v, want every document as before: %v", got, all)
			}
		})
	}
}

func TestADocumentFromAHiddenConversationIsNotFoundByID(t *testing.T) {
	reader := hidingTh()
	r := documentsRouter(t, newFourDocuments(), reader, nil)

	missing := serve(r, http.MethodGet, "/api/documents/doc-nope/carousel")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("an unknown document: %d %s", missing.Code, missing.Body.String())
	}
	for _, path := range []string{
		"/api/documents/doc-hidden/pages/1",
		"/api/documents/doc-hidden/carousel",
		// A PDF has no pages, and still answers as missing rather than as "no
		// such page" — which would confirm the id.
		"/api/documents/doc-hidden-2/pages/1",
	} {
		w := serve(r, http.MethodGet, path)
		if w.Code != missing.Code || w.Body.String() != missing.Body.String() {
			t.Errorf("GET %s: %d %s — want exactly what a document that does not exist gets: %d %s",
				path, w.Code, w.Body.String(), missing.Code, missing.Body.String())
		}
	}
	// A readable document and one with no conversation get past the check, to
	// this router's 503 for contents it cannot serve.
	for _, path := range []string{"/api/documents/doc-open/carousel", "/api/documents/doc-render/pages/1"} {
		if w := serve(r, http.MethodGet, path); w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s: %d %s, want past the check", path, w.Code, w.Body.String())
		}
	}
	if want := []string{"th-hidden", "th-hidden", "th-hidden", "th-open"}; !slices.Equal(reader.mayRead, want) {
		t.Errorf("asked about %v, want %v — and never about a document with no conversation", reader.mayRead, want)
	}
}

func TestTheLinksOfAHiddenDocumentAreAnsweredAsForADocumentWithNone(t *testing.T) {
	docs := newFourDocuments()
	shares := &linkedShares{}
	r := documentsRouter(t, docs, hidingTh(), app.NewReportShareService(shares, docs, nil, nil))

	none := serve(r, http.MethodGet, "/api/documents/doc-render/shares")
	hidden := serve(r, http.MethodGet, "/api/documents/doc-hidden/shares")
	if hidden.Code != http.StatusOK || hidden.Body.String() != none.Body.String() {
		t.Errorf("listing a hidden document's links: %d %s — want what a document nobody shared gets: %d %s",
			hidden.Code, hidden.Body.String(), none.Code, none.Body.String())
	}
	if open := serve(r, http.MethodGet, "/api/documents/doc-open/shares"); !strings.Contains(open.Body.String(), "sh-doc-open") {
		t.Errorf("a readable document's links were hidden too: %s", open.Body.String())
	}

	mint := serve(r, http.MethodPost, "/api/documents/doc-hidden/shares")
	if mint.Code != http.StatusNotFound || mint.Body.String() != `{"error":"document not found"}` {
		t.Errorf("minting a link on a hidden document: %d %s, want create's own not-found", mint.Code, mint.Body.String())
	}

	w := serve(r, http.MethodDelete, "/api/documents/doc-hidden/shares/sh-doc-hidden")
	if w.Code != http.StatusNoContent || !slices.Equal(shares.revoked, []string{"sh-doc-hidden"}) {
		t.Errorf("revoking a hidden document's link: %d, revoked %v — closing a door must never be gated", w.Code, shares.revoked)
	}
}

func TestADocumentCheckThatFailsServesNothing(t *testing.T) {
	docs := newFourDocuments()
	reader := hiddenConversations{err: errors.New("control database unreachable")}
	r := documentsRouter(t, docs, reader, app.NewReportShareService(&linkedShares{}, docs, nil, nil))
	for _, path := range []string{
		"/api/documents",
		"/api/documents/doc-open/carousel",
		"/api/documents/doc-open/pages/1",
		"/api/documents/doc-open/shares",
	} {
		w := serve(r, http.MethodGet, path)
		body := w.Body.String()
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(body, "could not check access") {
			t.Errorf("GET %s: %d %s, want the access check's 503", path, w.Code, body)
		}
		if strings.Contains(body, "unreachable") || strings.Contains(body, ".pdf") || strings.Contains(body, "sh-") {
			t.Errorf("GET %s leaked through a failed check: %s", path, body)
		}
	}
}
