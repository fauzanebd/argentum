package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// DashboardHandler exposes saved dashboard endpoints.
type DashboardHandler struct{ svc *app.DashboardService }

func NewDashboardHandler(svc *app.DashboardService) *DashboardHandler {
	return &DashboardHandler{svc: svc}
}

// Register installs the routes. Caller wraps with Auth middleware.
func (h *DashboardHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/dashboards", h.list)
	rg.DELETE("/dashboards/:id", h.delete)
}

func (h *DashboardHandler) list(c *gin.Context) {
	out, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"dashboards": out})
}

func (h *DashboardHandler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
