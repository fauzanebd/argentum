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
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// MetricsHandler is Settings → Metrics (T-06): the tenant's named numbers.
//
// Reads are open to members, narrowed by the metric's source (RIZ-27): a member
// asking a question benefits from the same authoritative number, but a metric is
// its source's data in another shape — its key names what the source holds
// (`payroll_cost`) and its sql_template names the tables — so a member not
// granted a restricted source is not shown the metrics on it either. An admin's
// reads stay whole, for the reason the source list's do (WithAccess).
// Writes are admin-only (enforced in cmd/api's policy), because a metric is a
// SELECT the agent runs unattended forever and defining one is a privileged act.
type MetricsHandler struct {
	svc *app.MetricService
	// access narrows a member's reads to the metrics whose source they may see
	// (RIZ-27). Nil reads every metric, as before.
	access MetricSourceAccess
}

// MetricSourceAccess is which sources a person may see — the question a
// metric's visibility reduces to. *authz.Authorizer satisfies it, and so does
// any ConnectionAccess, which is the same method.
type MetricSourceAccess interface {
	Visible(ctx context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error)
}

func NewMetricsHandler(svc *app.MetricService) *MetricsHandler {
	return &MetricsHandler{svc: svc}
}

// WithAccess narrows a member's metric list and metric-by-id read to the
// metrics on sources they may see (RIZ-27), asking about sources rather than
// metrics because a metric id is not a resource anybody is granted: the grant
// is on the data, and the metric inherits it.
//
// **A member, not an admin**, the line CompanyHandler.WithAccess draws for the
// source list and for the same reason: Settings → Metrics is where metrics are
// configured, every metric write is admin-only, and a metric missing from an
// ungranted admin's list would be one they could no longer edit or delete.
//
// What this does not touch is an agent. query_metric, list_metrics and the
// turn's metric catalog read the registry with no person to ask about — T-Z6's
// rule that a grant gates what a person is shown, never what an agent queries,
// pinned by TestAMetricOnARestrictedSourceStillAnswersAnAgent.
func (h *MetricsHandler) WithAccess(a MetricSourceAccess) *MetricsHandler {
	h.access = a
	return h
}

// Register installs the routes. The read/write split is applied by apiPolicy in
// cmd/api, not here.
func (h *MetricsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/metrics", h.list)
	rg.GET("/metrics/:id", h.get)
	rg.POST("/metrics", h.create)
	rg.PUT("/metrics/:id", h.update)
	rg.DELETE("/metrics/:id", h.remove)
	// The "Test" button: render and run an unsaved metric so an admin sees the
	// SQL and the number before committing. Admin-only like the writes — it runs
	// arbitrary tenant SQL, which a member reaches only through the agent.
	rg.POST("/metrics/test", h.test)
}

func (h *MetricsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metrics are not configured"})
	return true
}

// metricFail maps the service's sentinels onto status codes. A validation
// failure — a non-SELECT, a template that returns two rows, an injection payload
// the binding refused — arrives wrapped in ErrInvalidInput and answers 400 with
// the specific reason, which is the acceptance criterion the whole ticket turns
// on.
func metricFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such metric"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *MetricsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	metrics, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		metricFail(c, err)
		return
	}
	metrics, ok := h.visibleMetrics(c, metrics)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, MetricsResponse{
		Metrics: metrics,
		Grains: []domain.MetricGrain{
			domain.MetricGrainDay, domain.MetricGrainWeek, domain.MetricGrainMonth,
			domain.MetricGrainQuarter, domain.MetricGrainYear,
		},
		Units: []domain.MetricUnit{
			domain.MetricUnitCurrency, domain.MetricUnitCount,
			domain.MetricUnitPercent, domain.MetricUnitRatio,
		},
	})
}

func (h *MetricsHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	m, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		metricFail(c, err)
		return
	}
	// The same 404 as an id that does not exist, not the documents' 403: no page
	// links to a metric by id, so a refusal would only confirm to a member that
	// a metric they cannot see is there.
	visible, ok := h.visibleMetrics(c, []*domain.MetricDefinition{m})
	if !ok {
		return
	}
	if len(visible) == 0 {
		metricFail(c, domain.ErrNotFound)
		return
	}
	c.JSON(http.StatusOK, MetricResponse{Metric: m})
}

// visibleMetrics is a member's metrics narrowed to those whose source they may
// see, in one question for the page over the distinct sources, keeping the
// list's order; an admin's are all of them (WithAccess). A check that fails
// serves nothing — never the unfiltered list, which is the leak this closes.
//
// A metric whose source row is gone is hidden from a member, because authz reads
// an unknown id as refused, and still listed for an admin, who can delete it.
func (h *MetricsHandler) visibleMetrics(c *gin.Context, all []*domain.MetricDefinition) ([]*domain.MetricDefinition, bool) {
	role := domain.Role(c.GetString("role"))
	if h.access == nil || len(all) == 0 || role == domain.RoleAdmin {
		return all, true
	}
	seen := make(map[string]bool, len(all))
	ids := make([]string, 0, len(all))
	for _, m := range all {
		if !seen[m.SourceID] {
			seen[m.SourceID] = true
			ids = append(ids, m.SourceID)
		}
	}
	subject := authz.Subject{CompanyID: companyID(c), UserID: userID(c), Role: role}
	visible, err := h.access.Visible(c.Request.Context(), subject, domain.ResourceKindConnection, ids)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID(c)).
			Warn("metric source access check failed; serving no metric")
		middleware.AbortAccessCheckFailed(c)
		return nil, false
	}
	keep := make(map[string]bool, len(visible))
	for _, id := range visible {
		keep[id] = true
	}
	out := make([]*domain.MetricDefinition, 0, len(all))
	for _, m := range all {
		if keep[m.SourceID] {
			out = append(out, m)
		}
	}
	return out, true
}

func (h *MetricsHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.MetricInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), in)
	if err != nil {
		metricFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, MetricResponse{Metric: m})
}

func (h *MetricsHandler) update(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.MetricInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), in)
	if err != nil {
		metricFail(c, err)
		return
	}
	c.JSON(http.StatusOK, MetricResponse{Metric: m})
}

func (h *MetricsHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		metricFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *MetricsHandler) test(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.MetricInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ev, err := h.svc.Test(c.Request.Context(), companyID(c), in)
	if err != nil {
		metricFail(c, err)
		return
	}
	c.JSON(http.StatusOK, MetricTestResponse{
		Value: ev.Value, From: ev.From, To: ev.To, RenderedSQL: ev.RenderedSQL,
	})
}
