package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// KnowledgeDocumentsHandler is the upload surface for the PDFs a tenant wants
// this product to read (T-P1).
//
// **The routes are `/api/knowledge/documents`, not `/api/documents/…`.** That
// path is taken, by the documents this product *generates* — and the two are
// opposites: one is output addressed by thread, the other is input a tenant
// supplies. Hanging the upload off the same noun would have made every future
// reader disambiguate, and the frontend feature that consumes this is
// `features/knowledge/` for the same reason.
//
// Registered unconditionally, with a typed 503 when there is no object storage.
// The native dashboards handler beside it states the argument: a missing route
// reads as a wrong path, where a 503 tells a client why.
type KnowledgeDocumentsHandler struct {
	svc *app.DocumentIngestService
	// maxUploadBytes is the request-body cap. It duplicates the service's own
	// limit on purpose — this one stops a 900 MB body from being read at all,
	// where the service's stops it from being stored. Neither is redundant: the
	// service is called by tests and by any future non-HTTP path.
	maxUploadBytes int64
}

func NewKnowledgeDocumentsHandler(svc *app.DocumentIngestService, maxUploadMB int) *KnowledgeDocumentsHandler {
	if maxUploadMB <= 0 {
		maxUploadMB = 25
	}
	return &KnowledgeDocumentsHandler{svc: svc, maxUploadBytes: int64(maxUploadMB) << 20}
}

// Register installs the routes. Caller wraps with Auth middleware.
//
// Who may upload is admin, and it is a decision rather than a default: an
// applied document becomes data every member can query, which puts it on the
// line the cookbook and the audit log already sit on. Reading the list is a
// member's, because seeing what the workspace has ingested is not a privilege.
// The roadmap flags this as an owner's call
// (`docs/plan/06-pdf-knowledge-roadmap.md`, open question 2) and moving it is
// one line in `cmd/api/policy.go`.
func (h *KnowledgeDocumentsHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/knowledge/documents", h.upload)
	rg.GET("/knowledge/documents", h.list)
	rg.GET("/knowledge/documents/:id", h.get)
	rg.DELETE("/knowledge/documents/:id", h.remove)
}

func (h *KnowledgeDocumentsHandler) upload(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	// The body cap is installed before the multipart reader touches it, so an
	// oversized upload is refused while it is still bytes on a socket rather
	// than after it has been buffered. gin's own MaxMultipartMemory only decides
	// where the parts are buffered, not how large they may be.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxUploadBytes)

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected a PDF in the \"file\" field"})
		return
	}
	if file.Size > h.maxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": "the document must be " + strconv.FormatInt(h.maxUploadBytes>>20, 10) + " MB or smaller",
		})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the uploaded file"})
		return
	}
	defer func() { _ = f.Close() }()

	out, err := h.svc.Upload(c.Request.Context(), app.UploadInput{
		CompanyID: companyID(c),
		UserID:    userID(c),
		Filename:  file.Filename,
		Body:      f,
	})
	if err != nil {
		knowledgeDocumentFail(c, err)
		return
	}
	// 200 for a file we already hold, 202 for one that has been accepted and not
	// yet read. Neither is 201: the thing a client cares about is not that a row
	// exists, it is whether anything is going to happen to it.
	status := http.StatusAccepted
	if out.Deduplicated {
		status = http.StatusOK
	}
	c.JSON(status, out)
}

func (h *KnowledgeDocumentsHandler) list(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	docs, err := h.svc.List(c.Request.Context(), companyID(c), limit, offset)
	if err != nil {
		knowledgeDocumentFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"documents": docs})
}

func (h *KnowledgeDocumentsHandler) get(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	doc, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		knowledgeDocumentFail(c, err)
		return
	}
	c.JSON(http.StatusOK, doc)
}

func (h *KnowledgeDocumentsHandler) remove(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		knowledgeDocumentFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *KnowledgeDocumentsHandler) unavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "document upload is not configured on this deployment",
	})
}

// knowledgeDocumentFail maps the service's errors onto status codes. A document
// belonging to another tenant is a 404 for suggestionFail's reason: a 403 would
// confirm the row is real to a caller holding a bare uuid.
func knowledgeDocumentFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such document"})
	case errors.Is(err, app.ErrDocumentTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
