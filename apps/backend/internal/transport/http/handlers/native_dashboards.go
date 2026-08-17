package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// NativeDashboardsHandler serves dashboards this product executes itself
// (T-D10): the stored spec, and the resolved data behind it.
//
// Reads are open to members, because opening a dashboard is the thing a member
// is here to do and the numbers are the company's own. Deleting is admin-only
// (enforced by apiPolicy in cmd/api, not here) — a dashboard is a dozen panels
// somebody's Monday depends on, which is a different act from clearing a chat
// thread.
//
// There is deliberately no create or update route in this release. A dashboard
// is authored by the agent through create_dashboard (T-D11), which is one code
// path with one set of validation rules; a second authoring surface would be a
// second place for those rules to drift before there is a UI that needs it.
type NativeDashboardsHandler struct{ svc *app.DashboardService }

func NewNativeDashboardsHandler(svc *app.DashboardService) *NativeDashboardsHandler {
	return &NativeDashboardsHandler{svc: svc}
}

// Register installs the routes. Caller wraps with Auth middleware; the
// member/admin split is applied by apiPolicy in cmd/api.
func (h *NativeDashboardsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/dashboards", h.list)
	rg.GET("/dashboards/:id", h.get)
	// The data route is separate from the definition on purpose: opening a
	// dashboard runs a dozen queries against a tenant warehouse, and a client
	// that only wants the title should not have to.
	rg.GET("/dashboards/:id/data", h.data)
	rg.DELETE("/dashboards/:id", h.remove)
}

func (h *NativeDashboardsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dashboards are not configured"})
	return true
}

// dashboardFail maps the service's sentinels onto status codes. A filter value a
// viewer got wrong is ErrInvalidInput and answers 400 naming the filter — after
// T-D13 that value comes off a query string somebody edited by hand, and "500"
// would tell them nothing about which one.
func dashboardFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such dashboard"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *NativeDashboardsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	out, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		dashboardFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"dashboards": out})
}

func (h *NativeDashboardsHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		dashboardFail(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// data resolves the dashboard for this viewer.
//
// Filter values arrive as ordinary query parameters and are matched against the
// spec's declared filters by name; anything else in the query string is ignored
// rather than merged, because a parameter the dashboard never declared is not a
// filter. `refresh` is accepted and currently does nothing — the panel cache is
// T-D8 — and it is read here rather than passed through so it cannot be mistaken
// for a filter named "refresh".
func (h *NativeDashboardsHandler) data(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	req := map[string]string{}
	for key, values := range c.Request.URL.Query() {
		if key == "refresh" || len(values) == 0 {
			continue
		}
		req[key] = values[0]
	}
	res, err := h.svc.Resolve(c.Request.Context(), companyID(c), c.Param("id"), req)
	if err != nil {
		dashboardFail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *NativeDashboardsHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		dashboardFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
