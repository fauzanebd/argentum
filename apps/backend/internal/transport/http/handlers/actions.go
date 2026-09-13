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
type ActionsHandler struct {
	svc *app.ActionService
	// conversations hides a proposal raised in a conversation the caller may not
	// read (T-Z14). Nil leaves every route as it was.
	conversations ConversationReader
}

func NewActionsHandler(svc *app.ActionService) *ActionsHandler {
	return &ActionsHandler{svc: svc}
}

// WithConversationAccess makes a proposal as hidden as the conversation it was
// raised in (T-Z14, the owner's decision on access-grants §13d).
//
// **Hidden from the list, not found by id, and not decided by anyone who may not
// read it.** A proposal carries what the conversation said — an email body, a
// caption — so listing it hid nothing that `T-Z10` hid; and approving an action
// without being able to read why it was proposed is deciding blind. The rule is
// the conversation's, so an admin without the agent's grant is hidden from too
// (decision 4): an action only admins may approve, raised in a conversation no
// admin is granted, waits until one grants themselves — and the grant is audited.
//
// A proposal raised outside any conversation has nothing to inherit and is
// listed and decided as before.
func (h *ActionsHandler) WithConversationAccess(r ConversationReader) *ActionsHandler {
	h.conversations = r
	return h
}

// readableProposals keeps the proposals whose conversation the caller may read,
// in order, with one readability check for the page.
func (h *ActionsHandler) readableProposals(c *gin.Context, invs []*domain.ActionInvocation) ([]*domain.ActionInvocation, error) {
	if h.conversations == nil || len(invs) == 0 {
		return invs, nil
	}
	var threads []string
	seen := map[string]bool{}
	for _, inv := range invs {
		if inv != nil && inv.ThreadID != "" && !seen[inv.ThreadID] {
			seen[inv.ThreadID] = true
			threads = append(threads, inv.ThreadID)
		}
	}
	if len(threads) == 0 {
		return invs, nil
	}
	readable, err := h.conversations.Readable(c.Request.Context(), companyID(c), userID(c), threads)
	if err != nil {
		return nil, err
	}
	ok := make(map[string]bool, len(readable))
	for _, id := range readable {
		ok[id] = true
	}
	out := make([]*domain.ActionInvocation, 0, len(invs))
	for _, inv := range invs {
		if inv != nil && (inv.ThreadID == "" || ok[inv.ThreadID]) {
			out = append(out, inv)
		}
	}
	return out, nil
}

// mayReachProposal answers whether the caller may open or decide one proposal,
// and writes the answer when they may not: the route's own not-found, byte for
// byte, so a hidden proposal and an id that never existed cannot be told apart —
// and a 503 when the check itself failed. **Before the role check**, so a hidden
// proposal does not reveal whether its kind is one the caller could decide.
//
// A proposal this lookup cannot find passes through to the route, which answers a
// missing one exactly as it did before.
func (h *ActionsHandler) mayReachProposal(c *gin.Context, id string) bool {
	if h.conversations == nil {
		return true
	}
	ctx := c.Request.Context()
	threadID, err := h.svc.ThreadOf(ctx, companyID(c), id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return true
	case err != nil:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that proposal; try again"})
		return false
	case threadID == "":
		return true
	}
	ok, err := h.conversations.MayRead(ctx, companyID(c), userID(c), threadID)
	switch {
	case err != nil:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that proposal; try again"})
		return false
	case !ok:
		actionFail(c, domain.ErrNotFound)
		return false
	}
	return true
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
	invs, err := h.svc.ListPending(c.Request.Context(), companyID(c), c.GetString("role"))
	if err != nil {
		actionFail(c, err)
		return
	}
	// Never the unfiltered list: a check that failed serves nothing (T-Z14).
	if invs, err = h.readableProposals(c, invs); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to these proposals; try again"})
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
	if h.unavailable(c) || !h.mayReachProposal(c, c.Param("id")) {
		return
	}
	inv, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"), c.GetString("role"))
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
	if !h.mayReachProposal(c, id) {
		return
	}

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
	// The caller got past the check above, so they may decide this kind — say so
	// on the way out too, or the card re-renders from this response with the
	// buttons it has just used disabled.
	inv.CanDecide = true
	c.JSON(http.StatusOK, ActionResponse{Action: inv})
}
