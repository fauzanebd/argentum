package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DashboardShareHandler is the authenticated half of T-D13: minting, listing
// and revoking links. The half that serves one is DashboardSharePageHandler,
// and they are separate types for the reason the report player's two are —
// one runs inside a session and the other runs for a stranger, and a single
// type would make it easy to add a method to the wrong half.
type DashboardShareHandler struct{ svc *app.DashboardShareService }

func NewDashboardShareHandler(svc *app.DashboardShareService) *DashboardShareHandler {
	return &DashboardShareHandler{svc: svc}
}

func (h *DashboardShareHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/dashboards/:id/shares", h.create)
	rg.GET("/dashboards/:id/shares", h.list)
	rg.DELETE("/dashboards/:id/shares/:shareID", h.revoke)
}

type createDashboardShareRequest struct {
	ExpiresInDays     int               `json:"expires_in_days"`
	LockedParams      map[string]string `json:"locked_params"`
	AllowFilters      bool              `json:"allow_filters"`
	Password          string            `json:"password"`
	MaxRefreshPerHour int               `json:"max_refresh_per_hour"`
}

func (h *DashboardShareHandler) create(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dashboard sharing is not configured"})
		return
	}
	var req createDashboardShareRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	out, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), app.CreateShareInput{
		DashboardID:       c.Param("id"),
		ExpiresInDays:     req.ExpiresInDays,
		LockedParams:      req.LockedParams,
		AllowFilters:      req.AllowFilters,
		Password:          req.Password,
		MaxRefreshPerHour: req.MaxRefreshPerHour,
	})
	if err != nil {
		shareFail(c, err)
		return
	}
	// The token is in this response and nowhere else, ever. It is not stored
	// and cannot be re-read, which the dashboard has to say out loud next to
	// the copy button.
	c.JSON(http.StatusCreated, out)
}

func (h *DashboardShareHandler) list(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusOK, gin.H{"shares": []any{}})
		return
	}
	out, err := h.svc.List(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		shareFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"shares": out})
}

func (h *DashboardShareHandler) revoke(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dashboard sharing is not configured"})
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), companyID(c), c.Param("shareID")); err != nil {
		shareFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func shareFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not complete the request"})
	}
}
