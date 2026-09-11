package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// MetricCoverageHandler serves the one number that says whether this tenant's
// answers stand on defined metrics or on SQL the model wrote for the occasion
// (T-F4).
//
// It lives beside the feedback routes and carries the same role — admin — for
// the reason those do: this is the workspace's aggregate quality data, not a
// thing any member acts on. (The roadmap said member; the routes it named as
// the precedent are admin, and matching them was the smaller surprise.)
type MetricCoverageHandler struct {
	svc *app.MetricCoverageService
}

func NewMetricCoverageHandler(svc *app.MetricCoverageService) *MetricCoverageHandler {
	return &MetricCoverageHandler{svc: svc}
}

func (h *MetricCoverageHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/quality/metric-coverage", h.coverage)
}

func (h *MetricCoverageHandler) coverage(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metric coverage is not configured on this deployment"})
		return
	}
	days, _ := strconv.Atoi(c.Query("days"))
	rep, err := h.svc.ForCompany(c.Request.Context(), companyID(c), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, MetricCoverageResponse{
		WindowDays: rep.Days,
		From:       rep.Coverage.From,
		Certified:  rep.Coverage.Certified,
		AdHoc:      rep.Coverage.AdHoc,
		Mixed:      rep.Coverage.Mixed,
		NoData:     rep.Coverage.NoData,
		Answered:   rep.Coverage.Answered(),
		Percent:    rep.Coverage.Percent(),
		AdHocTop:   rep.AdHocTop,
	})
}
