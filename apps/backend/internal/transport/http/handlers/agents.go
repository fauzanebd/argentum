package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// AgentsHandler is Settings → Agents (T-S1): the tenant's own roster of named
// agents, each a persona plus a tool and source allowlist.
//
// Reads are open to members because T-S3 puts this list in the chat picker.
// Every write is admin, on the same line policy.go draws for connections: an
// agent's allowlist is "what the agent can reach".
type AgentsHandler struct {
	svc *app.AgentService
	// gen is "Generate with AI" (T-B4), or nil where no LLM is wired. It is a
	// separate service because it spends money and reads two other tickets'
	// tables, none of which the roster's CRUD has any business reaching.
	gen *app.AgentGenerateService
}

// NewAgentsHandler constructs the handler. svc may be nil in stripped-down
// wirings; the routes then answer 503 rather than panicking.
func NewAgentsHandler(svc *app.AgentService) *AgentsHandler {
	return &AgentsHandler{svc: svc}
}

// WithGenerator installs the generate route's service (T-B4). Optional wiring:
// without it the route answers 503 and the create form renders the button as
// unavailable rather than as broken.
func (h *AgentsHandler) WithGenerator(gen *app.AgentGenerateService) *AgentsHandler {
	h.gen = gen
	return h
}

// Register installs the routes. Call after Auth; apiPolicy in cmd/api is what
// makes the writes admin-only.
func (h *AgentsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/agents", h.list)
	rg.POST("/agents", h.create)
	// Static segment on POST, where the only other entry is /agents itself —
	// there is no POST /agents/:id for it to compete with in gin's method tree.
	rg.POST("/agents/generate", h.generate)
	rg.GET("/agents/:id", h.get)
	rg.PUT("/agents/:id", h.update)
	rg.DELETE("/agents/:id", h.remove)
	rg.PUT("/agents/:id/default", h.setDefault)
}

// agentToolLabels is the sentence beside each checkbox. The *list* of tools
// comes from the live registry (tools.Registry) — this map only names them for
// a human, and a tool with no entry renders as its own identifier, which is
// worse-looking and still correct. What must never happen is a tool being
// absent from the form because a frontend array was not updated.
var agentToolLabels = map[string]string{
	"list_sources":         "List the workspace's databases",
	"get_schema":           "Read table and column names",
	"run_sql":              "Run read-only SQL queries",
	"create_visualization": "Build a chart from a query",
	"create_dashboard":     "Assemble charts into a dashboard",
	"schedule_task":        "Schedule a recurring question",
	"generate_document":    "Generate a PDF or slide deck",
}

// templateInfo projects the loaded gallery onto the wire (T-B3). The service
// has already narrowed each card's suggested tools to this deployment's
// registry; what happens here is only the field-by-field choice of what a
// browser gets.
func (h *AgentsHandler) templateInfo() []AgentTemplate {
	src := h.svc.Templates()
	out := make([]AgentTemplate, 0, len(src))
	for _, t := range src {
		out = append(out, AgentTemplate{
			Key:              t.Key,
			Name:             t.Name,
			Description:      t.Description,
			Persona:          t.Persona,
			SuggestedTools:   t.SuggestedTools,
			SourceHints:      t.SourceHints,
			StarterQuestions: t.StarterQuestions,
		})
	}
	return out
}

func (h *AgentsHandler) toolInfo() []AgentToolInfo {
	names := h.svc.ToolNames()
	out := make([]AgentToolInfo, 0, len(names))
	for _, n := range names {
		label := agentToolLabels[n]
		if label == "" {
			label = n
		}
		out = append(out, AgentToolInfo{Name: n, Label: label})
	}
	return out
}

// unavailable answers the wirings that have no roster service. Same shape the
// API keys handler uses: a reason, not a panic and not a 404.
func (h *AgentsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agents are not configured"})
	return true
}

// fail maps the service's sentinel errors onto status codes.
//
// ErrNotFound is 404 for another company's agent as much as for one that never
// existed: a 403 would confirm the row is real, and the id is a bare uuid in a
// URL. ErrConflict is 409 — "you cannot delete the last agent" is a state, not
// a malformed request, and a 400 would send an admin looking for a typo.
func agentFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such agent"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *AgentsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	agents, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		agentFail(c, err)
		return
	}
	c.JSON(http.StatusOK, AgentsResponse{
		Agents: agents, Tools: h.toolInfo(), Templates: h.templateInfo(),
		Generation: h.generationInfo(c),
	})
}

// generationInfo tells the form whether the Generate button can be pressed, and
// why not when it cannot (T-B4). It rides on the roster payload for the reason
// the tool list does: the same screen reads both, and a second request to learn
// whether a button is enabled is a round trip for a disabled state.
func (h *AgentsHandler) generationInfo(c *gin.Context) AgentGenerationInfo {
	if h.gen == nil {
		return AgentGenerationInfo{}
	}
	st := h.gen.State(c.Request.Context(), companyID(c))
	return AgentGenerationInfo{
		Available:        st.Available,
		CreditsExhausted: st.CreditsExhausted,
	}
}

// generate improves what is in the create form into a description and a persona
// (T-B4). It writes nothing: the tenant saves the form, or does not.
//
// 402 for an exhausted balance rather than 400 or 500, the same answer the chat
// routes give: the request was well-formed and the caller can fix it by topping
// up. The dashboard disables the button before it gets here, so a request that
// arrives is a tab that was open when the balance ran out.
func (h *AgentsHandler) generate(c *gin.Context) {
	if h.gen == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent generation is not configured"})
		return
	}
	var in app.AgentGenerateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.gen.Generate(c.Request.Context(), companyID(c), in)
	switch {
	case errors.Is(err, domain.ErrInsufficientCredits):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": app.CreditsExhaustedMessage})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, AgentGenerationResult{
			Description: out.Description,
			Persona:     out.Persona,
			Fallback:    out.Fallback,
		})
	}
}

func (h *AgentsHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	a, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		agentFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": a})
}

func (h *AgentsHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.AgentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.svc.Create(c.Request.Context(), companyID(c), in)
	if err != nil {
		agentFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"agent": a})
}

func (h *AgentsHandler) update(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.AgentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), in)
	if err != nil {
		agentFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": a})
}

func (h *AgentsHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		agentFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AgentsHandler) setDefault(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.SetDefault(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		agentFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
