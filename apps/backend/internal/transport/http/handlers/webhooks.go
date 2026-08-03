package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// WebhooksHandler is Settings → Webhooks (T-15): where a tenant says which
// events they want posted to their own server.
//
// **Every route is admin, including the reads**, on the same reasoning as MCP
// servers: a subscription is an egress destination this system will POST to
// unattended, and the list of them is a map of where a workspace's events go.
type WebhooksHandler struct {
	svc *app.WebhookSubscriptionService
}

func NewWebhooksHandler(svc *app.WebhookSubscriptionService) *WebhooksHandler {
	return &WebhooksHandler{svc: svc}
}

// Register installs the routes. Call after Auth; apiPolicy in cmd/api is what
// makes them admin-only.
func (h *WebhooksHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/webhooks", h.list)
	rg.POST("/webhooks", h.create)
	rg.PUT("/webhooks/:id", h.update)
	rg.DELETE("/webhooks/:id", h.remove)
}

func (h *WebhooksHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "outbound webhooks are not configured on this deployment"})
	return true
}

func webhookFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
	case errors.Is(err, domain.ErrInvalidInput):
		// The service wraps the egress guard's own sentence, so an admin reads
		// "10.0.0.5 is a private address" rather than "invalid URL" and stops
		// looking for a typo that is not there.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// list returns the subscriptions plus the event vocabulary, so the form offers
// what this deployment actually publishes rather than a hard-coded list that
// can go stale.
func (h *WebhooksHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	subs, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		webhookFail(c, err)
		return
	}
	if subs == nil {
		subs = []*domain.WebhookSubscription{}
	}
	c.JSON(http.StatusOK, WebhooksResponse{
		Subscriptions:    subs,
		Events:           domain.WebhookEvents(),
		DisableAfter:     domain.WebhookAutoDisableAfter,
		SignatureHeader:  "Argentum-Signature",
		SignatureMessage: "<unix timestamp>.<raw body>, HMAC-SHA256 with your workspace's webhook secret",
	})
}

type webhookSubscriptionReq struct {
	URL    string   `json:"url" binding:"required"`
	Events []string `json:"events"`
	// Enabled is only read by update. A created subscription is enabled: a
	// tenant filling in this form wants deliveries.
	Enabled *bool `json:"enabled"`
}

func (h *WebhooksHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req webhookSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sub, err := h.svc.Create(c.Request.Context(), companyID(c), req.URL, req.Events)
	if err != nil {
		webhookFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, sub)
}

func (h *WebhooksHandler) update(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req webhookSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// An update that omits `enabled` leaves the subscription on, which is what a
	// form editing the URL or the event list means. Switching one off is an
	// explicit false.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sub, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), req.URL, req.Events, enabled)
	if err != nil {
		webhookFail(c, err)
		return
	}
	c.JSON(http.StatusOK, sub)
}

func (h *WebhooksHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		webhookFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
