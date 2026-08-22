package handlers

import (
	"errors"
	"net/http"

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
type SkillsHandler struct{ svc *app.SkillService }

// NewSkillsHandler constructs the handler. svc may be nil in a wiring without
// the repository; the routes then answer 503 rather than panicking.
func NewSkillsHandler(svc *app.SkillService) *SkillsHandler {
	return &SkillsHandler{svc: svc}
}

// Register installs the routes. Call after Auth; apiPolicy in cmd/api is what
// makes them admin-only.
func (h *SkillsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/skills", h.list)
	rg.POST("/skills", h.create)
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

func (h *SkillsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	skills, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The caps travel with the list so the form's counters are the server's
	// numbers rather than a second copy that can drift from them.
	c.JSON(http.StatusOK, gin.H{
		"skills": skills,
		"limits": gin.H{
			"name_chars":        domain.MaxSkillNameChars,
			"when_to_use_chars": domain.MaxSkillWhenToUseChars,
			"body_chars":        domain.MaxSkillBodyChars,
			"per_company":       domain.MaxSkillsPerCompany,
		},
	})
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
	c.JSON(http.StatusOK, gin.H{
		"skill_ids": ids,
		"means":     bindingDescription(len(ids)),
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
