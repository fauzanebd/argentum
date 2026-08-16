package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// APIKeysHandler is the dashboard's half of T-13: an admin mints, lists and
// revokes machine credentials here. The keys themselves authenticate `/v1`,
// which is a different surface with a different middleware — nothing on this
// handler is reachable with a key.
//
// T-A5 added the other half of owning a key: seeing what it has been doing.
// `requests` is the recorder's read side, and it is nil on a deployment without
// the 032 migration — the list then returns keys with no stats block rather
// than failing, because "we cannot show you the traffic" must not become
// "you cannot manage your keys".
type APIKeysHandler struct {
	svc      *app.APIKeyService
	requests APIKeyTrafficReader
}

// APIKeyTrafficReader is the narrow half of domain.APIRequestRepository this
// handler needs — the two reads, and neither write.
type APIKeyTrafficReader interface {
	StatsByKey(ctx context.Context, companyID string, since time.Time) (map[string]*domain.APIKeyRequestStats, error)
	RecentErrors(ctx context.Context, companyID, keyID string, limit int) ([]*domain.APIRequestError, error)
}

// NewAPIKeysHandler constructs the handler. svc may be nil in stripped-down
// wirings; the routes then answer 503 rather than panicking.
func NewAPIKeysHandler(svc *app.APIKeyService) *APIKeysHandler {
	return &APIKeysHandler{svc: svc}
}

// WithTraffic gives the handler the `/v1` request record (T-A5). Additive, like
// V1MeHandler.WithWebhookSecrets: a wiring without it serves exactly what T-13
// served.
func (h *APIKeysHandler) WithTraffic(r APIKeyTrafficReader) *APIKeysHandler {
	h.requests = r
	return h
}

// statsWindow is the period the per-key counters cover. A day is the window an
// integrator is actually in when something breaks — "is it still failing?" —
// and it is echoed on every stats object so the number in the tab is never a
// count without a period.
const statsWindow = 24 * time.Hour

// errorListLimit is the ticket's own number: the last 50 non-2xx responses.
const errorListLimit = 50

// Register installs the routes. Call after Auth; the policy table in cmd/api
// is what makes them admin-only.
//
// `GET /api-keys/scopes` and `DELETE /api-keys/:id` do not collide: gin keeps
// one route tree per method, and no GET route under this prefix takes a
// parameter. `GET /api-keys/errors` (T-A5) keeps that true on purpose — the key
// it filters on arrives as `?key_id=`, not as a path segment, because a
// literal beside a wildcard in one method tree is the collision this comment
// exists to avoid.
func (h *APIKeysHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/api-keys", h.list)
	rg.GET("/api-keys/scopes", h.scopes)
	rg.GET("/api-keys/errors", h.errors)
	rg.POST("/api-keys", h.create)
	rg.DELETE("/api-keys/:id", h.revoke)
}

// scopeDescription is the sentence the dashboard shows beside a checkbox. It
// is served rather than hardcoded in the frontend so that a scope added on the
// backend — `T-A1` adds two — appears in the UI without a second edit, and so
// there is exactly one place where a capability is described to a human.
//
// That promise was half-kept until 2026-08-16. `T-14` added `read:data` and
// `write:visualizations` to the vocabulary and not to this map, so the dashboard
// offered two checkboxes with a blank sentence beside them — and the blank one
// was on the widest read capability a key can carry, arbitrary SQL over every
// table the connection can see. Found by the 2026-08-16 §1d gate, which read
// `GET /api/api-keys/scopes` while minting a key it needed for something else
// (`docs/coverage/live-gate-backlog.md` §1d). `TestEveryScopeHasADescription` is
// what makes the next omission a failing test rather than a blank checkbox.
var scopeDescription = map[domain.Scope]string{
	domain.ScopeReadMetrics:   "Read the metric registry.",
	domain.ScopeReadThreads:   "Read conversation threads and their messages.",
	domain.ScopeReadUsage:     "Read token usage and the credit balance.",
	domain.ScopeReadAudit:     "Read the agent action log, including the SQL the agent ran.",
	domain.ScopeReadDocuments: "List generated documents and get fresh download links.",
	// The one that reads the warehouse itself rather than anything Argentum
	// recorded about it, which is why the sentence says so: a key holding this
	// can run any SELECT the connection permits, over every table it can see.
	domain.ScopeReadData:            "Query the workspace's connected databases over MCP: list the sources, read a table's schema, and run read-only SQL against any table the connection can see.",
	domain.ScopeWriteChat:           "Ask the agent a question. This spends the workspace's credits.",
	domain.ScopeWriteActions:        "Execute an action on the workspace's behalf.",
	domain.ScopeWriteReports:        "Generate a report. Rendering a supplied spec is free; asking the agent to write one spends credits.",
	domain.ScopeWriteVisualizations: "Create a Metabase chart or dashboard. It writes to Metabase, never to the workspace's own systems.",
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

// list returns the roster and, beside it, what each key has been doing.
//
// The stats ride along with the list rather than sitting on a route of their
// own — the same call the tab already makes, and the same reasoning as the tool
// vocabulary in `GET /api/agents`. A second round trip to decorate a list that
// is never rendered without the decoration is a request nobody should have to
// make.
//
// A failed stats read degrades to a list with no stats. The keys are the
// operational surface here: an admin who needs to revoke a leaked credential
// must not be blocked because a counters table is unreadable.
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
	c.JSON(http.StatusOK, APIKeysResponse{
		Keys:  keys,
		Stats: h.statsFor(c),
	})
}

// statsFor reads the window's counters, keyed by key id. Nil on any failure and
// on a wiring without the reader.
func (h *APIKeysHandler) statsFor(c *gin.Context) map[string]*domain.APIKeyRequestStats {
	if h.requests == nil {
		return nil
	}
	since := time.Now().UTC().Add(-statsWindow).Truncate(time.Hour)
	stats, err := h.requests.StatsByKey(c.Request.Context(), companyID(c), since)
	if err != nil {
		logrus.WithError(err).Warn("api key traffic stats unavailable")
		return nil
	}
	for _, s := range stats {
		s.WindowHours = int(statsWindow / time.Hour)
	}
	return stats
}

// errors serves the last 50 non-2xx `/v1` responses, optionally for one key.
//
// This is the route the ticket is really about: an integrator whose script got a
// 403 at 11pm reads it here, with the request id they were handed, instead of
// asking us to read a log. Admin-only through the policy table, like every
// other route on this handler — the error list names routes and error codes for
// every integration the company runs.
func (h *APIKeysHandler) errors(c *gin.Context) {
	if h.requests == nil {
		// Not a 503: the feature is absent on this deployment, and an empty list
		// with the window stated reads correctly in the tab. A 503 would put an
		// error banner on a page whose primary job — managing keys — works.
		c.JSON(http.StatusOK, APIKeyErrorsResponse{Errors: []*domain.APIRequestError{}, Limit: errorListLimit})
		return
	}
	rows, err := h.requests.RecentErrors(c.Request.Context(), companyID(c), c.Query("key_id"), errorListLimit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []*domain.APIRequestError{}
	}
	c.JSON(http.StatusOK, APIKeyErrorsResponse{Errors: rows, Limit: errorListLimit})
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
