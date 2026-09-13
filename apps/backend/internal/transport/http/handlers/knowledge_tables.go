package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/doctable"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// KnowledgeTablesHandler is the review surface's API: what was extracted from a
// document, what a reviewer decided about it, and the one button that publishes
// it (T-P6/T-P7).
//
// **Apply is a POST to its own route rather than a status field on a PATCH.**
// The roadmap's Decision 3 makes publishing a human act, and a human act should
// be a route somebody had to call on purpose — not a value a generic save could
// carry by accident. The same argument put `verify_status <> 'quarantined'` in
// the UPDATE's WHERE clause: this is the one place in the product where a
// mis-click writes data the agent will answer from.
type KnowledgeTablesHandler struct {
	svc *app.DocumentTableService
	// pages serves the parse artifact a reviewer reads beside the grid. A
	// reviewer who cannot see the page cannot review the parse, which is the
	// ticket's sentence and the reason this handler serves page JSON at all.
	pages *app.DocumentPageService
	// access asks about the document behind a table (T-Z6). Nil asks nothing,
	// as before roadmap 12.
	access DocumentAccess
}

// DocumentAccess is who may read which uploaded document (T-Z6), for the two
// knowledge handlers: one question for a table's document, one load for a page
// of the list. *authz.Authorizer satisfies it.
type DocumentAccess interface {
	Decide(ctx context.Context, s authz.Subject, kind domain.ResourceKind, id string) (authz.Decision, error)
	Visible(ctx context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error)
}

func NewKnowledgeTablesHandler(svc *app.DocumentTableService, pages *app.DocumentPageService) *KnowledgeTablesHandler {
	return &KnowledgeTablesHandler{svc: svc, pages: pages}
}

// WithAccess makes the table routes ask about the document a table came from
// (T-Z6).
//
// **They cannot ask through resourcePolicy.** A table is served under its own id,
// so a `(kind, param)` entry has nothing to name, and the classification test
// cannot see these routes as document routes at all (access-grants §9e item 6).
// Left alone, restricting a document would leave its tables one URL away. So the
// handler resolves the table to its document and asks there, before a table is
// read or written — and answers with the body RequireResource would have.
//
// The document routes beside them (`/documents/:id/tables`, `/pages/:page`) carry
// the document's id and are resourcePolicy's.
func (h *KnowledgeTablesHandler) WithAccess(a DocumentAccess) *KnowledgeTablesHandler {
	h.access = a
	return h
}

// Register installs the routes. Caller wraps with Auth middleware.
func (h *KnowledgeTablesHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/knowledge/documents/:id/tables", h.list)
	rg.GET("/knowledge/documents/:id/pages/:page", h.page)
	rg.GET("/knowledge/tables/:tableId", h.get)
	rg.PATCH("/knowledge/tables/:tableId", h.update)
	rg.POST("/knowledge/tables/:tableId/apply", h.apply)
	rg.POST("/knowledge/tables/:tableId/unpublish", h.unpublish)
}

func (h *KnowledgeTablesHandler) list(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	tables, err := h.svc.List(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tables": tables})
}

func (h *KnowledgeTablesHandler) get(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	if !h.mayReachTable(c, c.Param("tableId")) {
		return
	}
	table, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("tableId"))
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, table)
}

// mayReachTable answers whether the person may read the document a table was
// extracted from, and writes the refusal when they may not (T-Z6).
//
// The three outcomes are RequireResource's, for its reasons: a table this company
// does not have gets this handler's own 404; a restricted document not granted is
// a 403 naming the kind; and a check that fails is a 503 — never the table.
func (h *KnowledgeTablesHandler) mayReachTable(c *gin.Context, tableID string) bool {
	if h.access == nil {
		return true
	}
	documentID, err := h.svc.DocumentOf(c.Request.Context(), companyID(c), tableID)
	if err != nil {
		knowledgeTableFail(c, err)
		return false
	}
	subject := authz.Subject{CompanyID: companyID(c), UserID: userID(c), Role: domain.Role(c.GetString("role"))}
	d, err := h.access.Decide(c.Request.Context(), subject, domain.ResourceKindDocument, documentID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID(c), "table_id": tableID, "document_id": documentID,
		}).Warn("document access check failed; refusing the table")
		middleware.AbortAccessCheckFailed(c)
		return false
	}
	if d.Allowed || d.Reason == authz.ReasonNotFound {
		return true
	}
	// Recorded as the document it was refused, which is what was restricted —
	// the table is only where the person reached it from (T-Z9).
	authz.Record(c.Request.Context(), h.access, authz.Refusal{
		Subject: subject, Kind: string(domain.ResourceKindDocument), ResourceID: documentID, Reason: d.Reason,
		Door: authz.DoorDashboard, Channel: domain.ChannelDashboard,
	})
	middleware.AbortNotGranted(c, domain.ResourceKindDocument)
	return false
}

// page serves one page of the parse: its markdown, its word boxes and the
// rectangles the table candidates came from.
//
// Word boxes rather than an image, and that is a deliberate limit rather than a
// shortcut. Rendering the page would mean the sidecar rasterising it, which is
// T-P3's machinery and its egress argument; the boxes are already in the
// artifact, they are what the parser actually read, and a reviewer comparing
// the grid against them is comparing against the parse rather than against a
// picture of the page. What it cannot show is a stamp, a logo or a chart — and
// none of those becomes a column.
func (h *KnowledgeTablesHandler) page(c *gin.Context) {
	if h.pages == nil {
		h.unavailable(c)
		return
	}
	number, err := strconv.Atoi(c.Param("page"))
	if err != nil || number < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the page must be a positive number"})
		return
	}
	page, err := h.pages.Page(c.Request.Context(), companyID(c), c.Param("id"), number)
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// updateTableRequest is a reviewer's decision about one table.
type updateTableRequest struct {
	Title string `json:"title"`
	// Columns is the whole list, positionally. Not a patch of one column: a
	// partial update would have to match columns by name, and a reviewer
	// renaming a column is exactly when that matching is wrong.
	Columns []doctable.Column `json:"columns"`
}

func (h *KnowledgeTablesHandler) update(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	// Before the body is read, as well as before the write: the answer is the
	// table re-derived from the document, and a refused person must learn nothing
	// of it from a validation error either.
	if !h.mayReachTable(c, c.Param("tableId")) {
		return
	}
	var req updateTableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the request body is not a table update"})
		return
	}
	table, err := h.svc.UpdateColumns(c.Request.Context(), companyID(c), c.Param("tableId"), req.Title, req.Columns)
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, table)
}

func (h *KnowledgeTablesHandler) apply(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	// Publishing answers with the table it published, which is the document's
	// contents; asked before it happens, not after.
	if !h.mayReachTable(c, c.Param("tableId")) {
		return
	}
	table, err := h.svc.Apply(c.Request.Context(), companyID(c), c.Param("tableId"), userID(c))
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, table)
}

// unpublish does **not** ask about the document (T-Z6). It answers with nothing
// and only withdraws rows the agent could read, so it can take access away and
// never grant it — the property resourceExempt's rows share, and the same reason
// revoking a dashboard's link is never gated.
func (h *KnowledgeTablesHandler) unpublish(c *gin.Context) {
	if h.svc == nil {
		h.unavailable(c)
		return
	}
	if err := h.svc.Unpublish(c.Request.Context(), companyID(c), c.Param("tableId")); err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *KnowledgeTablesHandler) unavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "document review is not configured on this deployment",
	})
}

// knowledgeTableFail maps the service's errors onto status codes.
//
// The quarantine refusal is a 409 rather than a 400: the request was correct
// and the state of the table is what forbids it, and the difference matters to
// the surface — a 400 asks a person to fix their input, where a 409 tells them
// to look at the page.
func knowledgeTableFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such table"})
	case errors.Is(err, app.ErrTableQuarantined):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrWarehouseUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrDocumentNotParsed):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
