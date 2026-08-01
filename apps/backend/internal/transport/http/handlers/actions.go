package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ActionsHandler is the human side of the action framework (T-11): the endpoints
// the dashboard's inline approval card calls to list what the agent has proposed
// and to approve or reject it. The agent proposes through propose_action (T-10)
// and never reaches here; only a human decision does.
//
// Reads are member — a proposal shows in the chat stream everyone in the thread
// sees, so the pending list has to be member-visible for the card to render.
// Approve and reject are member in the coarse policy table and refined per kind
// here: a company_actions row's allowed_roles names who may decide that kind, and
// a caller outside it gets a 403 the card renders as read-only.
type ActionsHandler struct{ svc *app.ActionService }

func NewActionsHandler(svc *app.ActionService) *ActionsHandler {
	return &ActionsHandler{svc: svc}
}

// Register installs the routes. The member/admin split is applied by apiPolicy in
// cmd/api; the per-kind allowed_roles refinement is applied in decide below.
func (h *ActionsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/actions/pending", h.pending)
	// Configuration (admin) — which kinds are enabled and how. Registered before
	// the /:id routes so "config" is never captured as an invocation id.
	rg.GET("/actions/config", h.listConfig)
	rg.PUT("/actions/config/:kind", h.configure)
	rg.GET("/actions/:id", h.get)
	rg.POST("/actions/:id/approve", h.approve)
	rg.POST("/actions/:id/reject", h.reject)
}

func (h *ActionsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "actions are not configured"})
	return true
}

// actionFail maps the service's sentinels onto status codes. ErrActionExpired is
// a 409 like a double-decide, because both are "the proposal is no longer in a
// state you can act on" — the card shows the reason rather than retrying.
func actionFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such action proposal"})
	case errors.Is(err, domain.ErrActionExpired):
		c.JSON(http.StatusConflict, gin.H{"error": "this proposal has expired; ask the agent to propose it again"})
	case errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "this proposal has already been decided"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// ActionsResponse is the pending list. Wrapped rather than a bare array for the
// same reason WatchersResponse is: the shape stays extensible, and the generated
// TS type is an object the dashboard destructures.
type ActionsResponse struct {
	Actions []*domain.ActionInvocation `json:"actions"`
}

// ActionResponse is one invocation, returned by get and by a decision so the
// card can reflect the outcome from the same shape it listed.
type ActionResponse struct {
	Action *domain.ActionInvocation `json:"action"`
}

func (h *ActionsHandler) pending(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	invs, err := h.svc.ListPending(c.Request.Context(), companyID(c))
	if err != nil {
		actionFail(c, err)
		return
	}
	c.JSON(http.StatusOK, ActionsResponse{Actions: invs})
}

// ActionConfigResponse is the Settings → Actions payload: each configured kind
// and the full list of kinds this deployment can run, so the UI can offer a kind
// that has no row yet.
type ActionConfigResponse struct {
	Configured []*domain.CompanyAction `json:"configured"`
	Available  []string                `json:"available"`
}

func (h *ActionsHandler) listConfig(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	cfg, err := h.svc.ListConfig(c.Request.Context(), companyID(c))
	if err != nil {
		actionFail(c, err)
		return
	}
	c.JSON(http.StatusOK, ActionConfigResponse{Configured: cfg, Available: h.svc.AvailableKinds()})
}

func (h *ActionsHandler) configure(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.ActionConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := h.svc.ConfigureAction(c.Request.Context(), companyID(c), c.Param("kind"), userID(c), in)
	if err != nil {
		actionFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": cfg})
}

func (h *ActionsHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	inv, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		actionFail(c, err)
		return
	}
	c.JSON(http.StatusOK, ActionResponse{Action: inv})
}

func (h *ActionsHandler) approve(c *gin.Context) {
	h.decide(c, true)
}

func (h *ActionsHandler) reject(c *gin.Context) {
	h.decide(c, false)
}

// decide runs the per-kind role check the coarse policy cannot, then approves or
// rejects. The check is before the state transition on purpose: a member without
// the role for this kind must not be able to move the proposal even by racing.
func (h *ActionsHandler) decide(c *gin.Context, approve bool) {
	if h.unavailable(c) {
		return
	}
	cid, uid, role := companyID(c), userID(c), c.GetString("role")
	id := c.Param("id")

	ok, err := h.svc.PermittedToDecide(c.Request.Context(), cid, id, role)
	if err != nil {
		actionFail(c, err)
		return
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "your role is not permitted to decide this action"})
		return
	}

	var inv *domain.ActionInvocation
	if approve {
		inv, err = h.svc.Approve(c.Request.Context(), cid, id, uid)
	} else {
		inv, err = h.svc.Reject(c.Request.Context(), cid, id, uid)
	}
	if err != nil {
		actionFail(c, err)
		return
	}
	c.JSON(http.StatusOK, ActionResponse{Action: inv})
}
