package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// AccessHandler is the admin's half of resource grants (T-Z2): restrict a
// resource, say who may reach it, and read both back from either side.
//
// Every route is admin in apiPolicy, and none of them opens anything to the
// caller. They return who may reach an object, never what is in it, which is
// why an admin can manage a restricted dashboard they cannot themselves open
// (roadmap 12, decision 4) — and why T-Z3's classification of `:id` routes
// exempts these rather than putting them behind the grant they manage.
type AccessHandler struct {
	svc *app.ResourceAccessService
}

// NewAccessHandler constructs the handler. A nil service answers 503 on every
// route, which says why, where an absent route would read as a wrong path.
func NewAccessHandler(svc *app.ResourceAccessService) *AccessHandler {
	return &AccessHandler{svc: svc}
}

// Register installs the routes onto the authenticated group.
func (h *AccessHandler) Register(rg *gin.RouterGroup) {
	// Every resource of a kind at once (T-Z7). Settings → Team renders who may
	// reach each agent *and* what each person may reach from this one answer,
	// so the two directions an admin asks the question in are one read drawn
	// twice rather than two reads that a grant could land between.
	rg.GET("/access/:kind", h.list)
	rg.GET("/access/:kind/:id", h.view)
	rg.PUT("/access/:kind/:id/mode", h.setMode)
	rg.PUT("/access/:kind/:id/grants/:userID", h.grant)
	rg.DELETE("/access/:kind/:id/grants/:userID", h.revoke)
	// The other direction of the same data: what one person may reach. Beside
	// /users/:id/capabilities, because an admin looking at a person wants both.
	rg.GET("/users/:id/grants", h.forUser)
}

func (h *AccessHandler) unconfigured(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "resource access is not configured"})
	return true
}

func (h *AccessHandler) list(c *gin.Context) {
	if h.unconfigured(c) {
		return
	}
	views, err := h.svc.List(c.Request.Context(), companyID(c), c.Param("kind"))
	if err != nil {
		writeAccessError(c, err)
		return
	}
	if views == nil {
		views = []domain.ResourceAccessView{}
	}
	c.JSON(http.StatusOK, gin.H{"resources": views})
}

func (h *AccessHandler) view(c *gin.Context) {
	if h.unconfigured(c) {
		return
	}
	view, err := h.svc.View(c.Request.Context(), companyID(c), c.Param("kind"), c.Param("id"))
	if err != nil {
		writeAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

type setAccessModeReq struct {
	AccessMode string `json:"access_mode" binding:"required"`
}

func (h *AccessHandler) setMode(c *gin.Context) {
	if h.unconfigured(c) {
		return
	}
	var req setAccessModeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	change, err := h.svc.SetAccessMode(c.Request.Context(), companyID(c), userID(c), c.Param("kind"), c.Param("id"), req.AccessMode)
	if err != nil {
		writeAccessError(c, err)
		return
	}
	// 200 with a body where this was a 204 before T-Z5: restricting a dashboard
	// revokes its live share links, and the admin who pressed it is owed the
	// count rather than a silence that reads as "nothing else happened".
	c.JSON(http.StatusOK, change)
}

// grant is a PUT for grantCapability's reason: asking again for what is held is
// a 204, not a conflict.
func (h *AccessHandler) grant(c *gin.Context) {
	if h.unconfigured(c) {
		return
	}
	err := h.svc.Grant(c.Request.Context(), companyID(c), userID(c), c.Param("userID"), c.Param("kind"), c.Param("id"))
	if err != nil {
		writeAccessError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AccessHandler) revoke(c *gin.Context) {
	if h.unconfigured(c) {
		return
	}
	err := h.svc.Revoke(c.Request.Context(), companyID(c), userID(c), c.Param("userID"), c.Param("kind"), c.Param("id"))
	if err != nil {
		writeAccessError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AccessHandler) forUser(c *gin.Context) {
	if h.unconfigured(c) {
		return
	}
	grants, err := h.svc.ForUser(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		writeAccessError(c, err)
		return
	}
	if grants == nil {
		grants = []domain.ResourceGrant{}
	}
	c.JSON(http.StatusOK, gin.H{"grants": grants})
}

func writeAccessError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		// One sentence for a missing resource, a missing user, a malformed id
		// and another company's object alike: which of those it was is exactly
		// what a caller probing across tenants would want to learn.
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
