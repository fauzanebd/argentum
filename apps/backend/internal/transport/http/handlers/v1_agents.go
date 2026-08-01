package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
)

// V1AgentsHandler answers `GET /v1/agents`: the roster, as the integrator's
// backend sees it (T-S5).
//
// Without it `agent_id` is an unusable field. The ids live in the dashboard's
// Settings → Agents tab, so an integrator building a finance workflow would
// have to be handed a uuid out of band by somebody with an admin session — and
// re-handed one every time the tenant edits their roster. A `/v1` surface where
// the only reachable agent is "whatever the company default happens to be" is a
// surface on which the roster does not exist.
//
// It is deliberately read-only. Creating an agent is an admin act with a
// persona, a tool allowlist and a source allowlist behind it, and a machine
// credential that could mint one could mint one with every tool and every
// source ticked.
type V1AgentsHandler struct {
	roster V1RosterLister
	mcp    V1MCPServerLister
}

// V1RosterLister is the half of app.AgentService this route needs.
//
// Declared at the consumer, like V1BudgetReader and V1TurnUsageReader, and
// narrowed to one method for the reason above: the type this handler can name
// is the set of things it can be asked to do later.
type V1RosterLister interface {
	List(ctx context.Context, companyID string) ([]*domain.Agent, error)
}

// V1MCPServerLister resolves the company's MCP servers so an agent's bound ids
// can be published as `{id, name}` pairs (T-M3). Nil is legal — a deployment
// with no MCP registry omits the names and every agent's `mcp_servers` is `[]`.
// *app.MCPServerService satisfies it.
type V1MCPServerLister interface {
	List(ctx context.Context, companyID string) ([]*domain.MCPServer, error)
}

// NewV1AgentsHandler constructs the handler. roster may be nil; the route then
// answers a typed 503 rather than panicking, which is the pattern every other
// `/v1` handler follows — an absent route reads to an integrator as a wrong
// path. mcp may be nil independently — an agent then lists no server names.
func NewV1AgentsHandler(roster V1RosterLister, mcp V1MCPServerLister) *V1AgentsHandler {
	return &V1AgentsHandler{roster: roster, mcp: mcp}
}

// Register installs the route on a group already carrying APIKeyAuth.
//
// **No scope, on the same terms as `GET /v1/me`.** The ticket's rule is "the
// same read scope `GET /v1/me` uses", and that is none: this is the call an
// integrator makes to find out which agents they may name, and gating it would
// mean a key that can send a chat turn cannot discover what to send it as.
// Nothing here is a secret from the key holder — the persona, the tool
// allowlist and the source allowlist all stay behind the dashboard's admin
// session, and what is published is the name of a thing the same key can
// already run a turn as.
//
// It is the third entry in `cmd/api`'s unscoped exemption list, and that list
// is a test rather than a convention.
func (h *V1AgentsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/agents", h.list)
}

// agentResponse is the public shape of one agent. Additive only.
//
// It is **not** domain.Agent. That struct carries the persona prompt, the tool
// allowlist and the source allowlist — the tenant's own words about their own
// business, plus a map of which database each agent can reach — and every one
// of them would become part of a published contract the moment one was
// serialized. What a caller needs in order to use the field is an id and enough
// to choose between them.
type agentResponse struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Name   string `json:"name"`
	// Description is the tenant's own one-liner. Absent when they wrote none,
	// which is common enough that a client should not have to render an empty
	// string as if it were a value.
	Description string `json:"description,omitempty"`
	// IsDefault answers the question the endpoint exists to raise: omitting
	// `agent_id` runs as exactly this row. Without it a caller comparing their
	// unpinned turns against the roster has no way to tell which one answered.
	IsDefault bool `json:"is_default"`
	// Enabled is false for an agent an admin has switched off. Listed rather
	// than filtered out: naming a disabled agent is a 404, and an integrator
	// whose call started failing should be able to see *why* rather than watch
	// an id vanish from a list.
	Enabled bool `json:"enabled"`
	// MCPServers names the tenant MCP servers this agent may call (T-M3).
	// Choosing an agent over `/v1` is choosing a capability set, and this is the
	// visible half of it — the tools themselves stay behind the admin session,
	// but which integrations an agent can reach is the thing an integrator picks
	// between. Always present, `[]` when the agent is bound to none, which for
	// this list means it reaches no MCP server at all.
	MCPServers []mcpServerRef `json:"mcp_servers"`
}

// mcpServerRef is the published shape of one bound server: enough to recognise
// it, nothing that is a credential. No URL, no auth, no probe state.
type mcpServerRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func agentBody(a *domain.Agent, mcpNames map[string]string) agentResponse {
	// Always a non-nil slice so the field marshals to `[]` rather than null: a
	// client should not have to distinguish "no servers" from "field absent".
	servers := make([]mcpServerRef, 0, len(a.MCPServerIDs))
	for _, id := range a.MCPServerIDs {
		// A binding cascades away when its server is deleted, so an unresolved id
		// here is not expected — but if one appears (a server deleted between the
		// roster read and the name read), it is dropped rather than published as
		// a nameless id, which would read as a capability the agent does not have.
		if name, ok := mcpNames[id]; ok {
			servers = append(servers, mcpServerRef{ID: id, Name: name})
		}
	}
	return agentResponse{
		ID:          a.ID,
		Object:      "agent",
		Name:        a.Name,
		Description: a.Description,
		IsDefault:   a.IsDefault,
		Enabled:     a.Enabled,
		MCPServers:  servers,
	}
}

// abortAgentNotFound is the one answer every refused `agent_id` gets, on both
// write doors.
//
// **404 and never 403.** The three refusals underneath it are unknown, another
// company's, and disabled — and a 403 on the second is an existence oracle
// across tenants: an id is a bare uuid in a JSON body, and a caller who could
// tell "not yours" from "not real" could enumerate another company's roster one
// guess at a time. The status code *is* the oracle, which is the same reason
// `loadThread` answers 404 for a dashboard thread.
//
// `param` is set because the caller does have something to fix: a client that
// hard-coded an id the tenant has since deleted should be pointed at the field,
// and `GET /v1/agents` is where the current ones are.
func abortAgentNotFound(c *gin.Context) {
	apierr.AbortParam(c, apierr.TypeNotFound, "agent_not_found",
		"No such agent for this company. List them with `GET /v1/agents`.", "agent_id")
}

// list is `GET /v1/agents` — the company's roster, default first.
//
// It answers in the standard page envelope with `has_more: false`, always. A
// roster is bounded by what an admin can be bothered to configure and the
// repository has no keyset over it, so there is nothing to page — but a list
// route that answered a bare array would be the one `/v1` list an integrator
// has to special-case, and adding pagination later to a shape that never had it
// is the breaking change this package exists to avoid.
func (h *V1AgentsHandler) list(c *gin.Context) {
	if h.roster == nil {
		apierr.Abort(c, apierr.TypeServer, "agents_unavailable",
			"The agent roster is not available on this deployment.")
		return
	}
	agents, err := h.roster.List(c.Request.Context(), companyID(c))
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID(c)).Error("list api agents")
		apierr.Abort(c, apierr.TypeServer, "list_failed", "The agent roster could not be read.")
		return
	}
	// One read of the company's servers, turned into an id→name map the bodies
	// resolve their bindings against. A failure here degrades to no names rather
	// than failing the roster: the ids are still enforced at turn time, and an
	// integrator would rather see the agents than a 500 because the MCP registry
	// hiccuped.
	mcpNames := h.mcpNames(c)
	items := make([]agentResponse, 0, len(agents))
	for _, a := range agents {
		items = append(items, agentBody(a, mcpNames))
	}
	c.JSON(http.StatusOK, apiv1.NewPage(items, false, ""))
}

// mcpNames builds the id→name map for the company's MCP servers, or nil when
// there is no lister or the read fails.
func (h *V1AgentsHandler) mcpNames(c *gin.Context) map[string]string {
	if h.mcp == nil {
		return nil
	}
	servers, err := h.mcp.List(c.Request.Context(), companyID(c))
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID(c)).
			Warn("list api agents: could not read mcp servers; agents will list no server names")
		return nil
	}
	names := make(map[string]string, len(servers))
	for _, s := range servers {
		names[s.ID] = s.Name
	}
	return names
}
