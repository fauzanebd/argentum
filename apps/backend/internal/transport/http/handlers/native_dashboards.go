package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
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
type NativeDashboardsHandler struct {
	svc *app.DashboardService
	// access narrows the list to the dashboards the person reading it may open
	// (T-Z5). Nil lists every dashboard, as before roadmap 12.
	access DashboardAccess
}

// DashboardAccess is the one read the dashboard list makes about the person
// reading it (T-Z5). *authz.Authorizer is the production one.
type DashboardAccess interface {
	Visible(ctx context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error)
}

func NewNativeDashboardsHandler(svc *app.DashboardService) *NativeDashboardsHandler {
	return &NativeDashboardsHandler{svc: svc}
}

// WithAccess narrows the list by grant (T-Z5).
//
// **Only the list.** Opening one, and its data, ask through resourcePolicy in
// cmd/api like any route with a restrictable id, and refuse with a 403 that names
// the kind. The list cannot, because it carries no id; it asks here, once for the
// page.
//
// **An admin is narrowed exactly like a member**, unlike the agent roster. The
// roster is also Settings → Agents, so an admin is sent all of it; this list is
// only somewhere to open a dashboard from, and an entry an admin cannot open is
// not one. They manage a dashboard they are refused from Settings → Team, which
// reads its name off the access list rather than off this one.
func (h *NativeDashboardsHandler) WithAccess(a DashboardAccess) *NativeDashboardsHandler {
	h.access = a
	return h
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
	out, ok := h.openable(c, out)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"dashboards": out})
}

// openable is the dashboards the caller may open, in the order given. False
// means the response has been written.
//
// Nothing about a dashboard but its id goes in: not who created it, because
// created_by is provenance and not ownership (056:50), and not the caller's role
// (decision 4).
func (h *NativeDashboardsHandler) openable(c *gin.Context, all []*domain.Dashboard) ([]*domain.Dashboard, bool) {
	if h.access == nil || len(all) == 0 {
		return all, true
	}
	ids := make([]string, 0, len(all))
	for _, d := range all {
		ids = append(ids, d.ID)
	}
	subject := authz.Subject{CompanyID: companyID(c), UserID: userID(c), Role: domain.Role(c.GetString("role"))}
	visible, err := h.access.Visible(c.Request.Context(), subject, domain.ResourceKindDashboard, ids)
	if err != nil {
		logrus.WithError(err).WithField("company_id", subject.CompanyID).
			Warn("dashboard access check failed; refusing the list")
		// Refused rather than served unfiltered: the unfiltered list names the
		// dashboards this person was not supposed to be shown.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check dashboard access; try again"})
		return nil, false
	}
	allowed := make(map[string]bool, len(visible))
	for _, id := range visible {
		allowed[id] = true
	}
	out := make([]*domain.Dashboard, 0, len(visible))
	for _, d := range all {
		if allowed[d.ID] {
			out = append(out, d)
		}
	}
	return out, true
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
