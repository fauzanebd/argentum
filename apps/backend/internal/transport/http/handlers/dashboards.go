package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// SavedDashboardHandler exposes the saved-dashboard endpoints left from the
// Metabase era (006), kept read-mostly through the deprecation window (T-D15).
//
// It moved off `/api/dashboards` in T-D10, which the native dashboards now own.
// The rename is the honest one — these rows are `saved_dashboards`, pointers at
// objects in another system — and it keeps the two surfaces from arguing over
// one path during the deprecation window. Both this handler and its routes go in
// T-D15.
type DashboardHandler struct{ svc *app.SavedDashboardService }

func NewDashboardHandler(svc *app.SavedDashboardService) *DashboardHandler {
	return &DashboardHandler{svc: svc}
}

// Register installs the routes. Caller wraps with Auth middleware.
func (h *DashboardHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/saved-dashboards", h.list)
	rg.DELETE("/saved-dashboards/:id", h.delete)
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
