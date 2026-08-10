package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// EmbedKeysHandler is the dashboard's half of T-19: an admin mints, edits,
// revokes and lists the credentials their own website will hold.
//
// Nothing on this handler is reachable with an embed session — it authenticates
// with a staff JWT like every other `/api` route, and the policy table makes it
// admin-only. The reverse is the interesting direction and it is enforced in
// middleware.EmbedAuth, which sets no role at all.
type EmbedKeysHandler struct {
	svc *app.EmbedKeyService
	// config is the widget's appearance and content (T-23). It lives on this
	// handler rather than on a third one because an admin sets it on the same
	// tab, in the same visit, as the key it applies to.
	config domain.WidgetConfigStore
}

// NewEmbedKeysHandler constructs the handler. svc may be nil in stripped-down
// wirings; the routes answer 503 rather than panicking.
func NewEmbedKeysHandler(svc *app.EmbedKeyService) *EmbedKeysHandler {
	return &EmbedKeysHandler{svc: svc}
}

// WithConfig adds the widget configuration routes (T-23). Additive: a wiring
// without it serves exactly what T-19 served.
func (h *EmbedKeysHandler) WithConfig(store domain.WidgetConfigStore) *EmbedKeysHandler {
	h.config = store
	return h
}

func (h *EmbedKeysHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/embed-keys", h.list)
	rg.POST("/embed-keys", h.create)
	rg.PUT("/embed-keys/:id", h.update)
	rg.DELETE("/embed-keys/:id", h.revoke)
	// The widget's own look and words. A sibling of the keys rather than a
	// field on one: a tenant with two keys — a staging site and a production
	// one — has one widget, and duplicating the greeting per key would be two
	// places to change it and one place to forget.
	rg.GET("/embed-config", h.getConfig)
	rg.PUT("/embed-config", h.putConfig)
}

func (h *EmbedKeysHandler) getConfig(c *gin.Context) {
	if h.config == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured"})
		return
	}
	cfg, err := h.config.GetWidgetConfig(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The stored record, not WithDefaults(): this is the edit form, and showing
	// a tenant our defaults as though they had chosen them makes every unset
	// field look set. The widget's own route applies them.
	c.JSON(http.StatusOK, gin.H{"config": cfg})
}

func (h *EmbedKeysHandler) putConfig(c *gin.Context) {
	if h.config == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured"})
		return
	}
	var cfg domain.WidgetConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := cfg.Normalize(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.config.SaveWidgetConfig(c.Request.Context(), companyID(c), &cfg); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such company"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// No redeploy anywhere: the widget reads this on its next open, because
	// `GET /api/embed/config` is a live read rather than a build-time value.
	c.JSON(http.StatusOK, gin.H{"config": cfg})
}

type createEmbedKeyReq struct {
	Name string `json:"name" binding:"required"`
	// AllowedOrigins is required and cannot be `*` — enforced in
	// domain.NormalizeOrigins, where the error can explain itself. `binding`
	// only checks that the field arrived.
	AllowedOrigins []string `json:"allowed_origins" binding:"required"`
}

type updateEmbedKeyReq struct {
	AllowedOrigins []string `json:"allowed_origins" binding:"required"`
	// Enabled is a pointer so that omitting it is distinguishable from sending
	// false. A PUT that silently disabled a key because the client forgot a
	// field would take a tenant's site down for a reason nobody could see.
	Enabled *bool `json:"enabled"`
}

func (h *EmbedKeysHandler) list(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured"})
		return
	}
	keys, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if keys == nil {
		keys = []*domain.EmbedKey{}
	}
	// The session TTL rides along: the Embed tab shows it beside the install
	// snippet, because "how long does this token last?" is the first question
	// the integrator writing the re-sign loop has.
	c.JSON(http.StatusOK, gin.H{
		"keys":                        keys,
		"session_ttl_seconds":         int(h.svc.SessionTTL().Seconds()),
		"max_signature_lifetime_secs": int(app.EmbedMaxSignatureLifetime().Seconds()),
	})
}

func (h *EmbedKeysHandler) create(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured"})
		return
	}
	var req createEmbedKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), req.Name, req.AllowedOrigins)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The signing secret is in this response and in no other, ever. It is not
	// logged, and no read path can reconstruct it.
	c.JSON(http.StatusCreated, gin.H{"key": res.Key, "secret": res.Secret})
}

func (h *EmbedKeysHandler) update(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured"})
		return
	}
	var req updateEmbedKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	key, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), req.AllowedOrigins, enabled)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			// Also the answer for another company's key, and for one that has
			// been revoked — a revoked key is not editable back into service.
			c.JSON(http.StatusNotFound, gin.H{"error": "no such embed key"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key})
}

func (h *EmbedKeysHandler) revoke(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured"})
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such embed key"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
