package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// HTTPEndpointsHandler is Settings → HTTP endpoints (T-12b): the targets an
// http_action may call. Every route is admin, like MCP servers next door and for
// the same reason — a row is an egress destination plus a credential, a DSN-class
// object, and `POST /api/connections` drew this line first.
//
// There is no read of a single endpoint and no update: the header carries the
// credential and is never returned, and re-registering is how a target changes,
// so the surface is list, register, delete.
type HTTPEndpointsHandler struct{ svc *app.HTTPEndpointService }

// NewHTTPEndpointsHandler constructs the handler. svc may be nil in wirings with
// no cipher; the routes then answer 503 rather than panicking.
func NewHTTPEndpointsHandler(svc *app.HTTPEndpointService) *HTTPEndpointsHandler {
	return &HTTPEndpointsHandler{svc: svc}
}

// Register installs the routes. apiPolicy in cmd/api is what makes them admin-only.
func (h *HTTPEndpointsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/http-endpoints", h.list)
	rg.POST("/http-endpoints", h.create)
	rg.DELETE("/http-endpoints/:id", h.remove)
}

func (h *HTTPEndpointsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "HTTP endpoints are not configured"})
	return true
}

// httpEndpointFail maps the service's sentinels onto status codes. An egress
// refusal arrives wrapped in ErrInvalidInput and answers 400 with the guard's own
// sentence, so an admin who registered a private host reads why.
func httpEndpointFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such HTTP endpoint"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *HTTPEndpointsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	endpoints, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		httpEndpointFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"endpoints": endpoints,
		"methods":   h.svc.AvailableMethods(),
	})
}

func (h *HTTPEndpointsHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.HTTPEndpointInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ep, err := h.svc.Register(c.Request.Context(), companyID(c), userID(c), in)
	if err != nil {
		httpEndpointFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, ep)
}

func (h *HTTPEndpointsHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		httpEndpointFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
