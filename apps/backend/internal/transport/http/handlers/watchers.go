package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// WatchersHandler is Settings → Watchers (T-08): metric-condition triggers that
// fire an agent turn into a channel, unprompted.
//
// Reads are member — a watcher is company configuration, and its event history
// is the answer to "is this alert working?". Writes, the dry-run, and enabling
// are admin (enforced in cmd/api's policy): a watcher runs tenant SQL unattended
// forever and delivers to the company's channels, which is a privileged act.
type WatchersHandler struct{ svc *app.WatcherService }

func NewWatchersHandler(svc *app.WatcherService) *WatchersHandler {
	return &WatchersHandler{svc: svc}
}

// Register installs the routes. The read/write split is applied by apiPolicy in
// cmd/api, not here.
func (h *WatchersHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/watchers", h.list)
	rg.GET("/watchers/:id", h.get)
	rg.GET("/watchers/:id/events", h.events)
	rg.POST("/watchers", h.create)
	rg.PUT("/watchers/:id", h.update)
	rg.DELETE("/watchers/:id", h.remove)
	// The dry-run: evaluate the watcher over trailing periods without firing or
	// spending an LLM call, and record it so the watcher may be enabled. Admin,
	// because it runs tenant SQL — the same reason the metric Test button is.
	rg.POST("/watchers/:id/dry-run", h.dryRun)
}

func (h *WatchersHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "watchers are not configured"})
	return true
}

// watcherFail maps the service's sentinels onto status codes, like metricFail.
func watcherFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such watcher"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *WatchersHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	watchers, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		watcherFail(c, err)
		return
	}
	c.JSON(http.StatusOK, WatchersResponse{
		Watchers: watchers,
		Grains: []domain.WatcherGrain{
			domain.WatcherGrainDay, domain.WatcherGrainWeek, domain.WatcherGrainMonth,
		},
		Comparators: []domain.WatcherComparator{
			domain.WatcherComparatorGT, domain.WatcherComparatorLT,
			domain.WatcherComparatorPctChangeGT, domain.WatcherComparatorPctChangeLT,
			domain.WatcherComparatorNoData,
		},
		Channels: []domain.Channel{
			domain.ChannelDashboard, domain.ChannelWhatsApp,
			domain.ChannelDiscord, domain.ChannelLark, domain.ChannelSlack,
		},
		CompareOptions: []string{"previous_period", "same_period_last_year"},
	})
}

func (h *WatchersHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	w, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		watcherFail(c, err)
		return
	}
	c.JSON(http.StatusOK, WatcherResponse{Watcher: w})
}

func (h *WatchersHandler) events(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	// ?fired=true asks for the evaluations that delivered. The default is every
	// evaluation, because "why did this not message me?" is answered by the
	// suppressed rows and that question is at least as common as the other one.
	firedOnly := c.Query("fired") == "true"
	evs, err := h.svc.ListEvents(c.Request.Context(), companyID(c), c.Param("id"), limit, offset, firedOnly)
	if err != nil {
		watcherFail(c, err)
		return
	}
	c.JSON(http.StatusOK, WatcherEventsResponse{Events: evs})
}

func (h *WatchersHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.WatcherInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	w, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), in)
	if err != nil {
		watcherFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, WatcherResponse{Watcher: w})
}

func (h *WatchersHandler) update(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.WatcherInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	w, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), in)
	if err != nil {
		watcherFail(c, err)
		return
	}
	c.JSON(http.StatusOK, WatcherResponse{Watcher: w})
}

func (h *WatchersHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		watcherFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *WatchersHandler) dryRun(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	res, err := h.svc.DryRun(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		watcherFail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
