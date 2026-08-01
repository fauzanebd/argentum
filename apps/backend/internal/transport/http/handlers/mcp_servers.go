package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// MCPServersHandler is Settings → MCP servers (T-M1): the tenant's own servers,
// the tools discovery found on them, and the review that makes one callable.
//
// **Every route is admin, including the reads.** That is stricter than the
// agent roster next door, and the reason is what a row is: a bearer credential
// for a system we do not own plus an egress destination we will connect to. It
// is a DSN-class object, and `POST /api/connections` drew this line first.
type MCPServersHandler struct{ svc *app.MCPServerService }

// NewMCPServersHandler constructs the handler. svc may be nil in wirings that
// have no cipher or no client; the routes then answer 503 rather than panicking.
func NewMCPServersHandler(svc *app.MCPServerService) *MCPServersHandler {
	return &MCPServersHandler{svc: svc}
}

// Register installs the routes. Call after Auth; apiPolicy in cmd/api is what
// makes them admin-only.
func (h *MCPServersHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/mcp-servers", h.list)
	rg.POST("/mcp-servers", h.create)
	rg.GET("/mcp-servers/:id", h.get)
	rg.PUT("/mcp-servers/:id", h.update)
	rg.DELETE("/mcp-servers/:id", h.remove)
	// Discovery is explicit (locked decision 6): this is the button, and the
	// only other thing that probes is a save that changed the endpoint.
	rg.POST("/mcp-servers/:id/refresh", h.refresh)
	// The review that makes one tool callable. A PUT on the tool rather than a
	// bulk POST on the server, because approving is a decision per tool and a
	// batch endpoint invites a UI that approves the lot.
	rg.PUT("/mcp-servers/:id/tools/:toolId", h.reviewTool)
}

func (h *MCPServersHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MCP servers are not configured"})
	return true
}

// mcpFail maps the service's sentinels onto status codes.
//
// An egress refusal arrives wrapped in ErrInvalidInput and answers 400 with the
// guard's own sentence — "169.254.169.254 is a link-local address" tells an
// admin which rule they hit, and a bare "invalid URL" sends them to check for a
// typo that is not there.
func mcpFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such MCP server"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *MCPServersHandler) list(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	servers, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		mcpFail(c, err)
		return
	}
	c.JSON(http.StatusOK, MCPServersResponse{
		Servers:            servers,
		Transports:         []domain.MCPTransport{domain.MCPTransportHTTP, domain.MCPTransportSSE},
		AllowsInsecureHTTP: h.svc.AllowsInsecureHTTP(),
	})
}

func (h *MCPServersHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	srv, tools, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		mcpFail(c, err)
		return
	}
	c.JSON(http.StatusOK, mcpServerResponse(srv, tools))
}

func (h *MCPServersHandler) create(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.MCPServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	srv, tools, err := h.svc.Create(c.Request.Context(), companyID(c), in)
	if err != nil {
		mcpFail(c, err)
		return
	}
	// 201 even when the probe failed: the row exists, and the response carries
	// probe_error so the admin can see why the tool list is empty. A server
	// that is down at 4pm is not a bad request.
	c.JSON(http.StatusCreated, mcpServerResponse(srv, tools))
}

func (h *MCPServersHandler) update(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.MCPServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	srv, tools, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), in)
	if err != nil {
		mcpFail(c, err)
		return
	}
	c.JSON(http.StatusOK, mcpServerResponse(srv, tools))
}

func (h *MCPServersHandler) remove(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		mcpFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *MCPServersHandler) refresh(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	srv, tools, err := h.svc.Refresh(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		mcpFail(c, err)
		return
	}
	c.JSON(http.StatusOK, mcpServerResponse(srv, tools))
}

func (h *MCPServersHandler) reviewTool(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in struct {
		Approved bool `json:"approved"`
		ReadOnly bool `json:"read_only"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tools, err := h.svc.ReviewTool(c.Request.Context(), companyID(c),
		c.Param("id"), c.Param("toolId"), in.Approved, in.ReadOnly)
	if err != nil {
		mcpFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tools": mcpToolViews(tools)})
}

// mcpServerResponse is the one shape every route answers with, so a browser
// never has to reconcile two views of the same server.
func mcpServerResponse(srv *domain.MCPServer, tools []*domain.MCPServerTool) MCPServerResponse {
	return MCPServerResponse{Server: srv, Tools: mcpToolViews(tools)}
}

// mcpToolViews adds the one fact a browser cannot compute: whether an approved
// tool's text has changed since it was approved.
//
// Computed here rather than stored, because it is a comparison between two
// columns and a third column that could disagree with them is worse than no
// column at all.
func mcpToolViews(tools []*domain.MCPServerTool) []MCPToolView {
	out := make([]MCPToolView, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		out = append(out, MCPToolView{
			ID: t.ID, ServerID: t.ServerID, ToolName: t.ToolName,
			Description: t.Description, InputSchema: t.InputSchema,
			ReadOnly: t.ReadOnly, Approved: t.Approved,
			Drifted: t.Drifted(), DiscoveredAt: t.DiscoveredAt,
		})
	}
	return out
}
