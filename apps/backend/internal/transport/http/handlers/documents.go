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
}

func NewDocumentsHandler(docs domain.DocumentRepository, gen *docgen.Service) *DocumentsHandler {
	return &DocumentsHandler{docs: docs, gen: gen}
}

func (h *DocumentsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/documents", h.list)
	rg.GET("/documents/:id/pages/:page", h.page)
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
