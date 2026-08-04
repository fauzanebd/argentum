// Package mcpserver exposes Argentum's own tools over MCP, so a customer's
// agent — Claude Code, or one they wrote — can use them (T-14).
//
// It is the mirror image of internal/tools/mcp, and the two are easy to
// confuse. There, a tenant registers *their* server and our agent calls it;
// here, we are the server and *their* agent calls us. The test is who holds the
// credential: there it is theirs, here it is ours, issued as an API key.
//
// **Nothing in this package implements a tool.** It adapts `internal/tools` —
// the same instances the agent runs, already wrapped by the budget guard and
// the audit decorator — onto the MCP protocol. A tool that behaved differently
// over MCP than it does in a turn would be a bug with no owner, so the only way
// to add one here is to add it there.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ServerName and serverVersion identify us in the MCP handshake. A client's log
// is the first place somebody looks when a key starts making requests, and
// "unknown server" is a bad thing to find there.
const (
	ServerName    = "argentum"
	serverVersion = "1"
)

// exposed is the tool surface, and the scope each tool needs.
//
// It is a deliberate list rather than "every tool in the registry": the
// registry also holds `generate_document`, `schedule_task` and
// `propose_action`, and each of those either spends money, writes to a tenant's
// system, or produces an artifact somebody has to be told about. An MCP client
// is an agent we did not write, reasoning without our system prompt and without
// the guardrails a turn runs under — so what it gets is the read surface plus
// the two Metabase writes, and everything that changes the world stays behind a
// turn or behind `/v1`.
//
// A tool named here that this deployment does not run is simply absent, which
// is how `create_visualization` behaves where Metabase is unconfigured. That
// leniency is also how `list_watchers` sat here for a week naming a tool the
// registry has never held: absent-because-unconfigured and
// absent-because-imaginary look identical from in here. `Missing` is the
// difference, and `cmd/mcp` logs it at startup — see the 2026-08-04 gate in
// docs/coverage/mcp-server.md.
var exposed = map[string]domain.Scope{
	"list_sources":         domain.ScopeReadData,
	"get_schema":           domain.ScopeReadData,
	"run_sql":              domain.ScopeReadData,
	"list_metrics":         domain.ScopeReadMetrics,
	"query_metric":         domain.ScopeReadMetrics,
	"create_visualization": domain.ScopeWriteVisualizations,
	"create_dashboard":     domain.ScopeWriteVisualizations,
}

// ScopeFor returns the scope a tool needs, and whether it is exposed at all.
func ScopeFor(tool string) (domain.Scope, bool) {
	s, ok := exposed[tool]
	return s, ok
}

// ExposedTools is the surface, sorted — for the setup doc and for a test that
// wants to assert the list rather than read it.
//
// It is what this package *intends* to serve, not what a client will see: New
// serves the intersection with the registry it is handed. Use Missing to tell
// the two apart.
func ExposedTools() []string {
	out := make([]string, 0, len(exposed))
	for name := range exposed {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Missing returns the exposed names that the given registry does not hold,
// sorted. Every one of them is a tool the docs promise and no client can call.
//
// It cannot distinguish "this deployment has no Metabase" from "this name is a
// typo", and it does not try: both are worth saying out loud at startup, and
// the operator knows which of the two they are looking at.
func Missing(registry []interfaces.Tool) []string {
	have := make(map[string]struct{}, len(registry))
	for _, t := range registry {
		have[t.Name()] = struct{}{}
	}
	var out []string
	for name := range exposed {
		if _, ok := have[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// scopesKey is how an authenticated request hands the key's scopes to a tool
// handler. Unexported and typed, like tenantctx's keys, so nothing outside this
// package can plant one.
type scopesKey struct{}

// WithScopes puts the authenticated key's scopes on the context. The HTTP
// middleware calls it; the handlers below read it.
func WithScopes(ctx context.Context, scopes []domain.Scope) context.Context {
	return context.WithValue(ctx, scopesKey{}, scopes)
}

func scopesFrom(ctx context.Context) []domain.Scope {
	s, _ := ctx.Value(scopesKey{}).([]domain.Scope)
	return s
}

// New builds the MCP server over a tool registry.
//
// The tools are whatever the caller was handed — in production the stack's
// wrapped registry, so every call through here is budget-guarded and writes an
// `agent_actions` row exactly as a turn's call does. This function adds no
// wrapping of its own, deliberately: a second decorator chain would be a second
// place for the audit rule to be wrong.
func New(tools []interfaces.Tool) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: ServerName, Version: serverVersion}, nil)
	for _, tool := range tools {
		scope, ok := ScopeFor(tool.Name())
		if !ok {
			continue
		}
		srv.AddTool(describe(tool, scope), handlerFor(tool, scope))
	}
	return srv
}

// describe renders one tool's MCP declaration. The description carries the
// required scope because a client's operator reads it when a call is refused,
// and "which scope do I need?" is otherwise a support conversation.
func describe(tool interfaces.Tool, scope domain.Scope) *sdk.Tool {
	return &sdk.Tool{
		Name:        tool.Name(),
		Description: fmt.Sprintf("%s\n\nRequires the %s scope on your API key.", tool.Description(), scope),
		InputSchema: schemaFor(tool.Parameters()),
	}
}

// handlerFor adapts one tool. Two things happen before the tool runs: the
// scope check, and nothing else — the arguments go through untouched, because
// the tool's own parser is what the agent's calls use and a second parser here
// would be a second thing to disagree.
func handlerFor(tool interfaces.Tool, scope domain.Scope) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if !hasScope(scopesFrom(ctx), scope) {
			// Returned as a tool error rather than a transport error: the client's
			// agent reads it, and "your key is missing read:data" is something it
			// can relay to a human who can fix it.
			return errorResult(fmt.Sprintf("this API key does not carry the %s scope, which %s requires", scope, tool.Name())), nil
		}

		args := "{}"
		if len(req.Params.Arguments) > 0 {
			args = string(req.Params.Arguments)
		}
		out, err := tool.Execute(ctx, args)
		if err != nil {
			// A tool that refused — a guardrail, a spent budget, a bad query — is
			// an answer, not a protocol failure. The client sees the reason and
			// can try something else, which is exactly what the agent does with
			// the same string.
			return errorResult(err.Error()), nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}, nil
	}
}

func errorResult(msg string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: msg}}}
}

func hasScope(held []domain.Scope, want domain.Scope) bool {
	return slices.Contains(held, want)
}

// schemaFor converts the SDK's parameter map into a JSON Schema object.
//
// It is the inverse of internal/tools/mcp's paramsFromSchema, and the two
// together are the whole protocol adaptation this product needs: one reads a
// tenant's schema into our parameter specs, this writes ours out as a schema.
func schemaFor(params map[string]interfaces.ParameterSpec) json.RawMessage {
	properties := map[string]any{}
	var required []string
	for name, spec := range params {
		properties[name] = propertyFor(spec)
		if spec.Required {
			required = append(required, name)
		}
	}
	// Sorted so the schema is byte-stable across boots. A client that caches a
	// tool list by hash should not see it change because a map iterated
	// differently.
	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		// A schema that will not marshal would panic inside AddTool. An empty
		// object is a tool the client can still call, which is the better
		// failure — and it cannot happen for the specs the registry holds.
		return json.RawMessage(`{"type":"object"}`)
	}
	return raw
}

func propertyFor(spec interfaces.ParameterSpec) map[string]any {
	prop := map[string]any{}
	if spec.Type != nil {
		prop["type"] = spec.Type
	} else {
		prop["type"] = "string"
	}
	if strings.TrimSpace(spec.Description) != "" {
		prop["description"] = spec.Description
	}
	if len(spec.Enum) > 0 {
		prop["enum"] = spec.Enum
	}
	if spec.Default != nil {
		prop["default"] = spec.Default
	}
	if spec.Items != nil {
		prop["items"] = propertyFor(*spec.Items)
	}
	return prop
}
