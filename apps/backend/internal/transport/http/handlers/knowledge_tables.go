package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/doctable"
	"github.com/fauzanebd/argentum/internal/domain"
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
}

func NewKnowledgeTablesHandler(svc *app.DocumentTableService, pages *app.DocumentPageService) *KnowledgeTablesHandler {
	return &KnowledgeTablesHandler{svc: svc, pages: pages}
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
	table, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("tableId"))
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, table)
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
	table, err := h.svc.Apply(c.Request.Context(), companyID(c), c.Param("tableId"), userID(c))
	if err != nil {
		knowledgeTableFail(c, err)
		return
	}
	c.JSON(http.StatusOK, table)
}

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
