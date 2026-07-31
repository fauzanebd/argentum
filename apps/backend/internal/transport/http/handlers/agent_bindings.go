package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// AgentBindingsHandler is the bindings table under Settings → Agents (T-S4):
// which agent answers in which Discord channel, Lark chat or WhatsApp number.
//
// Admin on read as well as on write, unlike the roster beside it. The roster is
// in every member's chat picker; a binding is routing configuration, and its
// rows are the identifiers of the company's own rooms.
type AgentBindingsHandler struct{ svc *app.AgentBindingService }

// NewAgentBindingsHandler constructs the handler. svc may be nil in
// stripped-down wirings; the routes then answer 503 rather than panicking.
func NewAgentBindingsHandler(svc *app.AgentBindingService) *AgentBindingsHandler {
	return &AgentBindingsHandler{svc: svc}
}

// Register installs the routes.
//
// `/agent-bindings` rather than `/agents/bindings` for the reason wire.go's
// AgentsResponse records: a static segment beside `/agents/:id` is a static
// segment competing with a wildcard in one method tree.
func (h *AgentBindingsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/agent-bindings", h.list)
	rg.POST("/agent-bindings", h.create)
	rg.DELETE("/agent-bindings/:id", h.remove)
}

func (h *AgentBindingsHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "channel bindings are not configured"})
	return true
}

func (h *AgentBindingsHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	bindings, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		agentFail(c, err)
		return
	}
	c.JSON(http.StatusOK, AgentBindingsResponse{
		Bindings: bindings,
		Channels: domain.BindableChannels,
	})
}

func (h *AgentBindingsHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.BindingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	b, err := h.svc.Create(c.Request.Context(), companyID(c), in)
	if err != nil {
		// agentFail maps ErrAlreadyExists to 409, which is the answer the unique
		// index produces for a second binding on one address.
		agentFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"binding": b})
}

func (h *AgentBindingsHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		agentFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
