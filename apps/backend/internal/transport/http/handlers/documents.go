package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

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
			CreatedAt: d.CreatedAt,
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
