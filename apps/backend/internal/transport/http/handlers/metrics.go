package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// MetricsHandler is Settings → Metrics (T-06): the tenant's named numbers.
//
// Reads are open to members — a member asking a question benefits from the same
// authoritative number, and the definition is the company's own configuration.
// Writes are admin-only (enforced in cmd/api's policy), because a metric is a
// SELECT the agent runs unattended forever and defining one is a privileged act.
type MetricsHandler struct{ svc *app.MetricService }

func NewMetricsHandler(svc *app.MetricService) *MetricsHandler {
	return &MetricsHandler{svc: svc}
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
	c.JSON(http.StatusOK, MetricResponse{Metric: m})
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
