package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// SkillsHandler is Settings → Skills (T-K1): the tenant's own written
// procedures and which agent is offered them.
//
// **Every route is admin, including the reads**, matching the MCP servers
// handler next door rather than the agent roster. The reason is what a body
// becomes: text that reaches the model unfenced, as this product's own
// instruction. A member who can write one can write a procedure that says
// "always exclude the Jakarta branch from revenue" and every agent in the
// workspace will follow it — which is the feature working as designed, and
// exactly why the write is an admin act.
type SkillsHandler struct {
	svc *app.SkillService
	// The two bounds this deployment composes with. Held here rather than read
	// from the package defaults, because a deployment that lowered
	// SKILL_INDEX_MAX_CHARS and a screen that reported the default would
	// disagree about which procedures are being offered — and the screen would
	// be the one somebody believed.
	maxLines int
	maxChars int
	// drafter is T-K7. Optional: a deployment with no light LLM answers 503 on
	// the one route rather than losing the whole surface.
	drafter *app.SkillDraftService
}

// NewSkillsHandler constructs the handler. svc may be nil in a wiring without
// the repository; the routes then answer 503 rather than panicking.
func NewSkillsHandler(svc *app.SkillService) *SkillsHandler {
	return &SkillsHandler{svc: svc}
}

// WithDrafter installs "draft from a conversation" (T-K7).
func (h *SkillsHandler) WithDrafter(d *app.SkillDraftService) *SkillsHandler {
	h.drafter = d
	return h
}

// WithIndexBounds tells the handler what this deployment's index bounds are.
// Zero on either falls back to the package default inside IndexCost, which is
// the same normalisation the runner does.
func (h *SkillsHandler) WithIndexBounds(maxLines, maxChars int) *SkillsHandler {
	h.maxLines, h.maxChars = maxLines, maxChars
	return h
}

// Register installs the routes. Call after Auth; apiPolicy in cmd/api is what
// makes them admin-only.
func (h *SkillsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/skills", h.list)
	rg.POST("/skills", h.create)
	// Before `/skills/:id`, because gin would otherwise route `preview` into
	// the id parameter and answer a 404 for a path that exists.
	rg.POST("/skills/preview", h.preview)
	rg.POST("/skills/draft", h.draft)
	rg.GET("/skills/:id", h.get)
	rg.PUT("/skills/:id", h.update)
	rg.DELETE("/skills/:id", h.remove)
	// The binding lives under the agent, not under the skill: an admin edits
	// "which procedures does this agent follow" while looking at the agent, and
	// the reverse ("which agents follow this") is a report rather than a form.
	rg.GET("/agents/:id/skills", h.agentBinding)
	rg.PUT("/agents/:id/skills", h.setAgentBinding)
}

func (h *SkillsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "skills are not configured"})
	return true
}

// skillReq is the write shape. `source`, `created_by` and the timestamps are
// deliberately absent: a tenant must not be able to label their own text
// `builtin:` and inherit the trust argument that belongs to a reviewed commit.
type skillReq struct {
	Name      string `json:"name"`
	WhenToUse string `json:"when_to_use"`
	Body      string `json:"body"`
	// Enabled defaults to true on create — a skill somebody just wrote is one
	// they want used, and the alternative is a form whose save appears to do
	// nothing. A pointer so an update that omits it does not silently disable.
	Enabled *bool `json:"enabled"`
}

func (r skillReq) toSkill(defaultEnabled bool) *domain.Skill {
	enabled := defaultEnabled
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return &domain.Skill{
		Name:      r.Name,
		WhenToUse: r.WhenToUse,
		Body:      r.Body,
		Enabled:   enabled,
		Source:    domain.SkillSourceTenant,
	}
}

// draft turns a finished conversation into three form fields (T-K7).
//
// **It answers 200 with a draft and never writes a row**, which is the API
// shape the trust argument requires rather than a convenience. A route that
// created the skill would make an LLM the author of text that later reaches
// every turn unfenced — undoing T-K2 from a file T-K2 never mentions. The save
// is `POST /skills`, behind the same admin session, on words a human has read.
func (h *SkillsHandler) draft(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if h.drafter == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "drafting is not configured on this deployment; write the procedure by hand",
		})
		return
	}
	var req struct {
		ThreadID string `json:"thread_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.ThreadID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "thread_id is required — a draft is written from one conversation"})
		return
	}
	draft, err := h.drafter.Draft(c.Request.Context(), companyID(c), strings.TrimSpace(req.ThreadID))
	if err != nil {
		h.failDraft(c, err)
		return
	}
	c.JSON(http.StatusOK, draft)
}

// failDraft maps the drafter's errors. Credit exhaustion is a 402 rather than a
// 500: the request was fine and the workspace is out of balance, which is
// something the person pressing the button can act on.
func (h *SkillsHandler) failDraft(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrInferenceSkipped):
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error": "this workspace is out of credits, so nothing was drafted; the form still saves what you write by hand",
		})
	case errors.Is(err, app.ErrSkillDraftEmpty):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "the model did not return a usable draft; write the procedure by hand or try again",
		})
	}
}

func (h *SkillsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	skills, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The index cost is composed by the same code a turn composes with, so
	// "you are over the bound and these three procedures are not being offered"
	// is a fact this screen states rather than one an admin infers from a log
	// line in production (T-K6). A failure to compute it is not a failure to
	// list: the list is what the screen is for.
	var index *app.SkillIndexCost
	if cost, err := h.svc.IndexCost(c.Request.Context(), companyID(c), h.maxLines, h.maxChars); err == nil {
		index = cost
	}
	// The caps travel with the list so the form's counters are the server's
	// numbers rather than a second copy that can drift from them.
	c.JSON(http.StatusOK, SkillsResponse{
		Skills: skills,
		Index:  index,
		Limits: SkillLimits{
			NameChars:      domain.MaxSkillNameChars,
			WhenToUseChars: domain.MaxSkillWhenToUseChars,
			BodyChars:      domain.MaxSkillBodyChars,
			PerCompany:     domain.MaxSkillsPerCompany,
		},
	})
}

// preview renders a draft as the model will see it, and saves nothing.
//
// **The two panes are the reason this endpoint exists rather than a client-side
// render.** A skill is a prompt, and the most useful thing a prompt author can
// be shown is the bytes — the index line that rides every turn, and the framed
// body `load_skill` returns, marker included. A dashboard that drew those
// itself would be a second implementation of the two things this feature is,
// and the day it drifted the preview would be reassuring somebody about text
// nobody sends.
//
// POST rather than GET because the draft is a body, and a body an author is
// still typing has newlines in it.
func (h *SkillsHandler) preview(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req skillReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Not refused when it breaks a cap. An author who has pasted too much needs
	// to see the counter and the sentence, not an error where their own words
	// were — `Refusal` carries what the save would say.
	c.JSON(http.StatusOK, h.svc.Preview(req.toSkill(true)))
}

func (h *SkillsHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	skill, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, skill)
}

func (h *SkillsHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req skillReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	skill, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), req.toSkill(true))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, skill)
}

func (h *SkillsHandler) update(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req skillReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	skill, err := h.svc.Update(c.Request.Context(), companyID(c), userID(c), c.Param("id"), req.toSkill(true))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, skill)
}

func (h *SkillsHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *SkillsHandler) agentBinding(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	ids, err := h.svc.AgentBinding(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	// `means` is served rather than left to the client, because this endpoint
	// returns an empty array for the state that means *everything* — the
	// opposite of what the identically-shaped MCP binding endpoint means by it.
	c.JSON(http.StatusOK, SkillBindingResponse{
		SkillIDs: ids,
		Means:    bindingDescription(len(ids)),
	})
}

func (h *SkillsHandler) setAgentBinding(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req struct {
		SkillIDs []string `json:"skill_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.SetAgentBinding(c.Request.Context(), companyID(c), c.Param("id"), req.SkillIDs); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// bindingDescription is the empty-means-everything rule, said out loud on the
// wire.
func bindingDescription(n int) string {
	if n == 0 {
		return "this agent is offered every enabled skill in the workspace"
	}
	return "this agent is offered only the listed skills"
}

// fail maps the service's errors. ErrSkillLimit is a 409 rather than a 400: the
// request is well-formed and the workspace is full, which is a different thing
// for a caller to do something about.
func (h *SkillsHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrSkillLimit):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{
			"error": "a skill with that name already exists in this workspace; the agent picks one by name, so two cannot share it",
		})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
