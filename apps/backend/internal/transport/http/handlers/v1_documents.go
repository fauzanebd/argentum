package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// DocumentContentStore is the slice of the storage adapter `/v1/documents`
// needs to stream an object back. Declared at the consumer, narrow, so the
// handler is testable without MinIO — the same shape branding.ObjectStore and
// docgen.ObjectStore take, for the same reason.
//
// A reader rather than a []byte: a deck with a dozen chart images is megabytes,
// and buffering every concurrent download in the API's heap is a memory
// footprint chosen by whoever is downloading.
type DocumentContentStore interface {
	StreamKey(ctx context.Context, key string) (io.ReadCloser, int64, error)
}

// V1DocumentsHandler serves the tenant's generated documents (T-A2).
//
// It is read-only. A document is produced by a report route or by the agent;
// there is no upload door, and adding one would make Argentum a file host
// rather than a report generator.
type V1DocumentsHandler struct {
	docs    domain.DocumentRepository
	gen     *docgen.Service
	content DocumentContentStore
}

func NewV1DocumentsHandler(docs domain.DocumentRepository, gen *docgen.Service, content DocumentContentStore) *V1DocumentsHandler {
	return &V1DocumentsHandler{docs: docs, gen: gen, content: content}
}

// Register installs the routes on a group already carrying APIKeyAuth. Every
// one names `read:documents`.
func (h *V1DocumentsHandler) Register(rg *gin.RouterGroup) {
	read := middleware.RequireScope(domain.ScopeReadDocuments)
	rg.GET("/documents", read, h.list)
	rg.GET("/documents/:id", read, h.get)
	rg.GET("/documents/:id/content", read, h.download)
}

// documentResponse is the public shape of a document. Additive only: a field
// removed or renamed here is a breaking change to a published contract, and
// the answer to needing one is `/v2`.
type documentResponse struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Filename  string `json:"filename"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
	Source    string `json:"source"`
	// ThreadID is absent for a document the render door produced — it has no
	// conversation behind it.
	ThreadID  string    `json:"thread_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// DownloadURL is presigned and short-lived. It is re-issued on every read
	// rather than stored, so a caller who saved one and came back an hour later
	// gets a working link without paying to regenerate the document.
	DownloadURL string     `json:"download_url,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// documentBody renders one document. url may be empty — a presign failure
// should still return the metadata, because a caller listing documents is not
// necessarily about to download one.
func documentBody(doc *domain.Document, url string, expiresAt time.Time) documentResponse {
	out := documentResponse{
		ID:          doc.ID,
		Object:      "document",
		Filename:    doc.Filename,
		Format:      string(doc.Format),
		SizeBytes:   doc.SizeBytes,
		Source:      string(doc.Source),
		ThreadID:    doc.ThreadID,
		CreatedAt:   doc.CreatedAt,
		DownloadURL: url,
	}
	if !expiresAt.IsZero() {
		t := expiresAt.UTC()
		out.ExpiresAt = &t
	}
	return out
}

// list is `GET /v1/documents` — cursor-paginated, filterable by format and
// date.
//
// No download URLs on the list. Presigning is a signature per row, and a
// caller paging a hundred documents to find one is not about to fetch all
// hundred; `GET /v1/documents/:id` issues the URL for the one they picked.
func (h *V1DocumentsHandler) list(c *gin.Context) {
	if h.docs == nil {
		apierr.Abort(c, apierr.TypeServer, "documents_unavailable",
			"Documents are not available on this deployment.")
		return
	}
	f := domain.DocumentFilter{}

	if raw := strings.TrimSpace(c.Query("format")); raw != "" {
		format := domain.DocumentFormat(strings.ToLower(raw))
		if !format.Valid() {
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_format",
				"`format` must be one of pdf, pptx, xlsx, csv.", "format")
			return
		}
		f.Format = format
	}
	var err error
	if f.From, err = parseTimeParam(c, "created_after"); err != nil {
		return
	}
	if f.To, err = parseTimeParam(c, "created_before"); err != nil {
		return
	}
	if raw := c.Query("limit"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n <= 0 {
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_limit",
				"`limit` must be a positive integer.", "limit")
			return
		}
		f.Limit = n
	}
	if cursor := c.Query("cursor"); cursor != "" {
		t, id, decErr := apiv1.DecodeCursor(cursor)
		if decErr != nil {
			// A caller who hand-built a cursor is told so rather than handed
			// page one, which would look like the walk restarting for no reason.
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_cursor",
				"That `cursor` is not one this API issued. Pass back the `next_cursor` from the previous page.", "cursor")
			return
		}
		f.CursorTime, f.CursorID = t, id
	}

	rows, hasMore, err := h.docs.ListByCompany(c.Request.Context(), companyID(c), f)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID(c)).Error("list documents")
		apierr.Abort(c, apierr.TypeServer, "list_failed", "The document list could not be read.")
		return
	}

	items := make([]documentResponse, 0, len(rows))
	for _, doc := range rows {
		items = append(items, documentBody(doc, "", time.Time{}))
	}
	next := ""
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = apiv1.EncodeCursor(last.CreatedAt, last.ID)
	}
	c.JSON(http.StatusOK, apiv1.NewPage(items, hasMore, next))
}

// parseTimeParam reads an RFC3339 query parameter, writing the envelope and
// returning an error when it will not parse.
func parseTimeParam(c *gin.Context, name string) (time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_timestamp",
			"`"+name+"` must be an RFC3339 timestamp, e.g. 2026-07-28T00:00:00Z.", name)
		return time.Time{}, err
	}
	return t, nil
}

// get is `GET /v1/documents/:id`. It re-presigns on every call.
func (h *V1DocumentsHandler) get(c *gin.Context) {
	doc, ok := h.load(c)
	if !ok {
		return
	}
	url, expiresAt := "", time.Time{}
	if h.gen != nil {
		signed, exp, err := h.gen.Presign(c.Request.Context(), doc)
		if err != nil {
			logrus.WithError(err).WithField("document_id", doc.ID).
				Warn("presign failed; returning the document without a URL")
		} else {
			url, expiresAt = signed, exp
		}
	}
	c.JSON(http.StatusOK, documentBody(doc, url, expiresAt))
}

// download is `GET /v1/documents/:id/content`.
//
// It streams the object rather than 302-ing to the presigned URL. A
// server-side HTTP client that does not follow redirects — which is a
// deliberate setting in plenty of them — would otherwise get a 302 body and
// write three hundred bytes of nothing to disk. The redirect is also a second
// hostname the caller's egress rules have to allow.
func (h *V1DocumentsHandler) download(c *gin.Context) {
	doc, ok := h.load(c)
	if !ok {
		return
	}
	if h.content == nil {
		apierr.Abort(c, apierr.TypeServer, "content_unavailable",
			"Document contents are not available on this deployment.")
		return
	}
	body, size, err := h.content.StreamKey(c.Request.Context(), doc.StorageKey)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"document_id": doc.ID,
			"storage_key": doc.StorageKey,
		}).Error("document content read failed")
		apierr.Abort(c, apierr.TypeServer, "content_failed",
			"The document could not be read from storage.")
		return
	}
	defer func() { _ = body.Close() }()

	// The size comes from the object store rather than from documents.size_bytes:
	// the row records what was uploaded, and the header has to describe what is
	// actually about to be written. They agree in every case that is not a bug,
	// and if they disagree the client should be told the truth about the stream
	// it is reading.
	c.Header("Content-Disposition", `attachment; filename="`+doc.Filename+`"`)
	c.DataFromReader(http.StatusOK, size, doc.Format.ContentType(), body, nil)
}

// load resolves `:id` inside the tenant boundary.
func (h *V1DocumentsHandler) load(c *gin.Context) (*domain.Document, bool) {
	if h.docs == nil {
		apierr.Abort(c, apierr.TypeServer, "documents_unavailable",
			"Documents are not available on this deployment.")
		return nil, false
	}
	doc, err := h.docs.GetForCompany(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			logrus.WithError(err).WithField("company_id", companyID(c)).Warn("document lookup failed")
		}
		// A cross-tenant id and a nonexistent id answer identically. Anything
		// else is an existence oracle over another company's documents.
		apierr.Abort(c, apierr.TypeNotFound, "document_not_found", "No such document for this company.")
		return nil, false
	}
	return doc, true
}
