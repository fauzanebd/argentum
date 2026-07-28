package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// APIKeysHandler is the dashboard's half of T-13: an admin mints, lists and
// revokes machine credentials here. The keys themselves authenticate `/v1`,
// which is a different surface with a different middleware — nothing on this
// handler is reachable with a key.
type APIKeysHandler struct{ svc *app.APIKeyService }

// NewAPIKeysHandler constructs the handler. svc may be nil in stripped-down
// wirings; the routes then answer 503 rather than panicking.
func NewAPIKeysHandler(svc *app.APIKeyService) *APIKeysHandler {
	return &APIKeysHandler{svc: svc}
}

// Register installs the routes. Call after Auth; the policy table in cmd/api
// is what makes them admin-only.
//
// `GET /api-keys/scopes` and `DELETE /api-keys/:id` do not collide: gin keeps
// one route tree per method, and no GET route under this prefix takes a
// parameter.
func (h *APIKeysHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/api-keys", h.list)
	rg.GET("/api-keys/scopes", h.scopes)
	rg.POST("/api-keys", h.create)
	rg.DELETE("/api-keys/:id", h.revoke)
}

// scopeDescription is the sentence the dashboard shows beside a checkbox. It
// is served rather than hardcoded in the frontend so that a scope added on the
// backend — `T-A1` adds two — appears in the UI without a second edit, and so
// there is exactly one place where a capability is described to a human.
var scopeDescription = map[domain.Scope]string{
	domain.ScopeReadMetrics:   "Read the metric registry.",
	domain.ScopeReadThreads:   "Read conversation threads and their messages.",
	domain.ScopeReadUsage:     "Read token usage and the credit balance.",
	domain.ScopeReadAudit:     "Read the agent action log, including the SQL the agent ran.",
	domain.ScopeReadDocuments: "List generated documents and get fresh download links.",
	domain.ScopeWriteChat:     "Ask the agent a question. This spends the workspace's credits.",
	domain.ScopeWriteActions:  "Execute an action on the workspace's behalf.",
	domain.ScopeWriteReports:  "Generate a report. Rendering a supplied spec is free; asking the agent to write one spends credits.",
}

func (h *APIKeysHandler) scopes(c *gin.Context) {
	type scopeInfo struct {
		Scope       domain.Scope `json:"scope"`
		Description string       `json:"description"`
		// Writes is what the UI groups on: the capabilities that change
		// something or spend money should not sit in the same block as the
		// reads.
		Writes bool `json:"writes"`
	}
	out := make([]scopeInfo, 0, len(domain.AllScopes))
	for _, s := range domain.AllScopes {
		out = append(out, scopeInfo{
			Scope:       s,
			Description: scopeDescription[s],
			// Read off the scope's own name rather than an enumerated pair.
			// The list was two scopes long when it was written and is four
			// now; an enumeration that has to be edited every time a write
			// scope is added is an enumeration that will one day quietly
			// file a write under "reads".
			Writes: strings.HasPrefix(string(s), "write:"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"scopes": out})
}

func (h *APIKeysHandler) list(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "API keys are not configured"})
		return
	}
	keys, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if keys == nil {
		keys = []*domain.APIKey{}
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

type createAPIKeyReq struct {
	Name   string   `json:"name" binding:"required"`
	Scopes []string `json:"scopes" binding:"required"`
	// ExpiresInDays of 0 means no expiry.
	ExpiresInDays int `json:"expires_in_days"`
}

func (h *APIKeysHandler) create(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "API keys are not configured"})
		return
	}
	var req createAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c),
		req.Name, req.Scopes, req.ExpiresInDays)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The token is in this response and in no other, ever. Nothing logs it and
	// no read path can reconstruct it.
	c.JSON(http.StatusCreated, gin.H{"key": res.Key, "token": res.Token})
}

func (h *APIKeysHandler) revoke(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "API keys are not configured"})
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Also the answer for a key that belongs to another company, and
			// for one that is already revoked.
			c.JSON(http.StatusNotFound, gin.H{"error": "no such key"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
