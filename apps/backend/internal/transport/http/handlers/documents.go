package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentsHandler lists a tenant's generated documents for the dashboard
// (T-V4).
//
// `/v1/documents` has done this for integrators since `T-A2`; the dashboard
// could not, because that surface authenticates with an API key and a session
// is refused there as flatly as a key is refused here. So the staff who
// generated a report had no way to see the list of what they had generated —
// documents existed only as links inside the chat thread that produced them,
// and a link somebody scrolled past was gone.
//
// It reuses the repository and the presigner rather than the `/v1` handler:
// the two surfaces answer in different shapes and the shared thing is the data
// underneath, which is exactly where sharing belongs.
type DocumentsHandler struct {
	docs domain.DocumentRepository
	gen  *docgen.Service
	// conversations hides a document produced in a conversation the person may
	// not read (T-Z11). Nil lists every document to every member, as before.
	conversations ConversationReader
}

func NewDocumentsHandler(docs domain.DocumentRepository, gen *docgen.Service) *DocumentsHandler {
	return &DocumentsHandler{docs: docs, gen: gen}
}

// WithConversationAccess hides a generated document from a person who may not
// read the conversation that produced it (T-Z11).
//
// T-Z10 hid the conversation; this hides what it made. A report HR's
// conversation generated is that conversation's answer written to a file, and
// leaving it on the documents page — download link and all — would restrict the
// question and publish the answer. **Hidden, not refused**, on T-Z10's terms:
// the list omits it, and every route naming it by id answers exactly as for a
// document that does not exist. A document with no conversation — what
// `POST /v1/reports/render` produces — has nothing to inherit a restriction from
// and is shown to everyone, as before.
func (h *DocumentsHandler) WithConversationAccess(r ConversationReader) *DocumentsHandler {
	h.conversations = r
	return h
}

// visibleDocuments narrows a page of documents to those whose conversation the
// caller may read: one readability check for the page, over each conversation
// once, however many documents it produced. A check that fails is an error, not
// the unfiltered page — "could not tell" must never read as "everything".
func visibleDocuments(c *gin.Context, r ConversationReader, docs []*domain.Document) ([]*domain.Document, error) {
	if r == nil || len(docs) == 0 {
		return docs, nil
	}
	var threads []string
	seen := map[string]bool{}
	for _, d := range docs {
		if d.ThreadID != "" && !seen[d.ThreadID] {
			seen[d.ThreadID] = true
			threads = append(threads, d.ThreadID)
		}
	}
	if len(threads) == 0 {
		return docs, nil
	}
	readable, err := r.Readable(c.Request.Context(), companyID(c), userID(c), threads)
	if err != nil {
		return nil, err
	}
	ok := make(map[string]bool, len(readable))
	for _, id := range readable {
		ok[id] = true
	}
	out := make([]*domain.Document, 0, len(docs))
	for _, d := range docs {
		if d.ThreadID == "" || ok[d.ThreadID] {
			out = append(out, d)
		}
	}
	return out, nil
}

// documentVisible reports whether the caller may open doc, and writes the answer
// when they may not: the routes' own "document not found", byte for byte, so a
// hidden document and an id that never existed cannot be told apart — and a 503
// when the check itself failed, because the document may well be theirs.
func documentVisible(c *gin.Context, r ConversationReader, doc *domain.Document) bool {
	if r == nil || doc.ThreadID == "" {
		return true
	}
	ok, err := r.MayRead(c.Request.Context(), companyID(c), userID(c), doc.ThreadID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that document; try again"})
		return false
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return false
	}
	return true
}

func (h *DocumentsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/documents", h.list)
	rg.GET("/documents/:id/pages/:page", h.page)
	rg.GET("/documents/:id/carousel", h.carousel)
}

type dashboardDocument struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	Format    string    `json:"format"`
	SizeBytes int64     `json:"size_bytes"`
	Source    string    `json:"source"`
	ThreadID  string    `json:"thread_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// DownloadURL is minted per read rather than stored, the same way
	// `/v1/documents/:id` does it: a presigned URL expires, and a saved one is
	// a link that stops working with no way to ask for another.
	DownloadURL string `json:"download_url,omitempty"`
	// Shareable says whether this document can be played as a deck. It is a
	// property of the format rather than a lookup: reading the object store
	// once per row to find out would turn a list into N round trips, and the
	// authoritative answer — the plan is there or it is not — is given by the
	// share route when somebody actually presses the button.
	Shareable bool `json:"shareable"`
	// PageCount is a carousel's slide count, absent for every other format
	// (T-G6). The pages themselves are one `GET /api/documents/:id/pages/:n`
	// each, through the same session as the list.
	PageCount int `json:"page_count,omitempty"`
}

func (h *DocumentsHandler) list(c *gin.Context) {
	docs, _, err := h.docs.ListByCompany(c.Request.Context(), companyID(c), domain.DocumentFilter{Limit: 50})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Filtered after the page is read, so it can come back shorter than fifty —
	// the thread list's trade-off (T-Z10), for the same reason: grants are not
	// pushed into the listing query.
	docs, err = visibleDocuments(c, h.conversations, docs)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to these documents; try again"})
		return
	}
	out := make([]dashboardDocument, 0, len(docs))
	for _, d := range docs {
		row := dashboardDocument{
			ID: d.ID, Filename: d.Filename, Format: string(d.Format),
			SizeBytes: d.SizeBytes, Source: string(d.Source), ThreadID: d.ThreadID,
			CreatedAt: d.CreatedAt, PageCount: d.PageCount,
			Shareable: d.Format == domain.DocumentFormatMP4 ||
				d.Format == domain.DocumentFormatPDF ||
				d.Format == domain.DocumentFormatPPTX,
		}
		if h.gen != nil {
			// A failed presign costs this row its link and nothing else. The
			// list is still the answer to "what have we generated", which is
			// most of why anyone opens it.
			if url, _, err := h.gen.Presign(c.Request.Context(), d); err == nil {
				row.DownloadURL = url
			}
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"documents": out})
}

// page is `GET /api/documents/:id/pages/:page`: one slide of a carousel, as
// JPEG, to the session that asked (T-G6, decision 6).
//
// An image in a persisted message cannot carry a presigned URL — the presign
// TTL is an hour and an `<img>` cannot be re-signed on click the way a link
// can — so the dashboard fetches pages through its API client, with the
// bearer header an `<img src>` cannot send, and this route serves them. It is
// company-scoped by the query (GetForCompany), so another tenant's id is a
// not-found rather than a comparison somebody has to remember; a page past the
// count is a not-found for the same reason a missing document is.
func (h *DocumentsHandler) page(c *gin.Context) {
	ctx := c.Request.Context()
	doc, err := h.docs.GetForCompany(ctx, companyID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	// Before the page number is read, so a hidden document's page past its
	// count and its first page are the same not-found.
	if !documentVisible(c, h.conversations, doc) {
		return
	}
	page, err := strconv.Atoi(c.Param("page"))
	if err != nil || page < 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such page"})
		return
	}
	if h.gen == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "document contents are not available on this deployment"})
		return
	}
	body, err := h.gen.LoadPage(ctx, doc, page)
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such page"})
		return
	}
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id":  doc.CompanyID,
			"document_id": doc.ID,
			"page":        page,
		}).Error("carousel page read failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "page could not be read"})
		return
	}
	// Private: the page is one tenant's figures, and a shared cache must not
	// serve it to the next session. An hour matches the presign TTL, so a
	// reload inside the hour costs nothing and a reload after it is one read.
	c.Header("Cache-Control", "private, max-age=3600")
	c.Data(http.StatusOK, "image/jpeg", body)
}

// carouselResponse is what the approval card and the documents row need to show
// a post before it is published (T-G7): the slides are N page requests, so this
// carries the words instead — the caption exactly as it would be pasted, its
// parts kept separate for a UI that wants to style them, and one alt per page.
type carouselResponse struct {
	// Caption is CaptionText's output: the text, a blank line, the hashtags.
	// Precomputed here rather than joined in the browser so "Copy caption"
	// copies the same string a channel was sent, rather than a second
	// assembly of it that can drift.
	Caption  string   `json:"caption"`
	Text     string   `json:"text,omitempty"`
	Hashtags []string `json:"hashtags,omitempty"`
	Alts     []string `json:"alts,omitempty"`
	Pages    int      `json:"pages"`
}

// carousel is `GET /api/documents/:id/carousel`: the manifest beside the pages.
//
// Company-scoped by the query for h.page's reason. A document that is not a
// carousel is a not-found rather than an empty body, because the caller asking
// for one has already decided from the row's format that this is a post.
func (h *DocumentsHandler) carousel(c *gin.Context) {
	ctx := c.Request.Context()
	doc, err := h.docs.GetForCompany(ctx, companyID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	if !documentVisible(c, h.conversations, doc) {
		return
	}
	if h.gen == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "document contents are not available on this deployment"})
		return
	}
	m, err := h.gen.LoadManifest(ctx, doc)
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "this document is not a carousel"})
		return
	}
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id":  doc.CompanyID,
			"document_id": doc.ID,
		}).Error("carousel manifest read failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "manifest could not be read"})
		return
	}
	// Private for the page route's reason: a caption is the tenant's copy, and
	// a shared cache must not hand it to the next session.
	c.Header("Cache-Control", "private, max-age=3600")
	c.JSON(http.StatusOK, carouselResponse{
		Caption:  docgen.CaptionText(m),
		Text:     m.Caption,
		Hashtags: m.Hashtags,
		Alts:     m.Alts,
		Pages:    m.Pages,
	})
}
