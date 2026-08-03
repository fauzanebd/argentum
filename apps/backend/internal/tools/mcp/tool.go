// Package mcptools turns a tenant's approved MCP tools into things the agent can
// call at turn time (T-M2).
//
// It lives under internal/tools rather than beside the CRUD in internal/app for
// the same reason the static registry does: a tool is something the worker runs
// inside the provider's tool-calling loop, and everything here is built so that
// an MCP tool is indistinguishable from run_sql to everything downstream — the
// budget guard bounds it, the audit decorator records it, and the SDK dispatches
// it by name. The one thing it adds is that the set is per-company and per-turn,
// which is what Source exists to resolve.
//
// The package name is mcptools, not mcp, so it does not collide with
// internal/adapters/mcp — the client this package calls through. Two packages,
// one protocol: the adapter speaks MCP over the wire, this one presents the
// result as an interfaces.Tool.
package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	adaptersmcp "github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// NamePrefix is the reserved namespace every MCP tool name carries. It is what
// keeps a tenant tool called `run_sql` from shadowing ours: our tools never
// start with it, so the two name spaces cannot intersect (locked into T-M2's
// "a tenant's run_sql cannot shadow ours"). It is also how the agent_service's
// allowlist validation recognises a per-company name it cannot enumerate
// statically, which is why it is exported.
const NamePrefix = "mcp__"

// maxNameLen is the Anthropic tool-name ceiling. A namespaced name past it is
// truncated rather than rejected: a tool the model cannot address is worse than
// one with a clipped name, and the server id on the audit row is what actually
// identifies the call.
const maxNameLen = 64

// callGuard bounds how many MCP calls one turn may make, across all of that
// turn's MCP tools. One guard is shared by every tool Source builds for a turn,
// so a turn that fans out across three servers is still bounded as a whole. It
// is a backstop beside T-16's token and tool-call budgets, aimed at the failure
// they do not catch cheaply: a server that answers fast and small, called in a
// tight loop, spends a turn's wall-clock on network round trips.
type callGuard struct {
	mu        sync.Mutex
	remaining int
}

func newCallGuard(maxCalls int) *callGuard {
	if maxCalls <= 0 {
		// A non-positive cap disables the backstop; the token/tool-call budget
		// still bounds the turn. Used by callers that pass 0 deliberately.
		maxCalls = 1 << 30
	}
	return &callGuard{remaining: maxCalls}
}

// take reserves one call, or reports that the turn is out of them.
func (g *callGuard) take() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.remaining <= 0 {
		return false
	}
	g.remaining--
	return true
}

// Tool is one approved, read-only, in-scope MCP tool, presented as an
// interfaces.Tool. It holds everything a call needs so Execute makes no
// database read — the scope, the approval and the drift check were all decided
// when Source built it, once for the turn.
type Tool struct {
	serverID   string
	serverName string
	rawName    string // the tenant's own name for the tool, sent to their server
	name       string // the namespaced name the model sees and the SDK dispatches on
	desc       string
	params     map[string]interfaces.ParameterSpec

	caller    Caller
	meter     Meter
	url       string
	transport domain.MCPTransport
	token     string
	timeout   time.Duration
	maxBytes  int
	calls     *callGuard
}

// Meter is the metering half, narrowed the way docgen.Meter is: one method,
// satisfied by *app.UsageService, so this package does not depend on the whole
// UsageRecorder interface for a kind of event none of the other tools record.
type Meter interface {
	RecordMCPCall(ctx context.Context, companyID, threadID, serverID, toolName string)
}

// nopMeter is what an unmetered process gets — the same shape tools.nopRecorder
// takes, so a stack built without a control database still runs its tools.
type nopMeter struct{}

func (nopMeter) RecordMCPCall(context.Context, string, string, string, string) {}

// Caller is the half of the MCP client this package needs: run one tool. An
// interface so the tool is testable without a server, and so the guarded client
// is something Source is given rather than something a tool could rebuild
// without the egress guard on it.
type Caller interface {
	CallTool(ctx context.Context, url string, transport domain.MCPTransport, token, toolName string,
		args map[string]any, maxBytes int) (adaptersmcp.CallResult, error)
}

// Ensure Tool satisfies the SDK contract and exposes its server id to the audit
// decorator (through the Unwrap chain — see tools.WithAudit).
var _ interfaces.Tool = (*Tool)(nil)

func (t *Tool) Name() string        { return t.name }
func (t *Tool) Description() string { return t.desc }

func (t *Tool) Parameters() map[string]interfaces.ParameterSpec { return t.params }

// MCPServerID is what the audit decorator records as the row's mcp_server_id.
// It is discovered through the Unwrap chain rather than promoted, because the
// budget guard and the audit wrapper both embed interfaces.Tool and an
// interface embed does not surface a method the interface does not declare.
func (t *Tool) MCPServerID() string { return t.serverID }

func (t *Tool) Run(ctx context.Context, input string) (string, error) { return t.Execute(ctx, input) }

// Execute calls the tenant's tool and returns its text.
//
// Every failure here is one the agent recovers from, never one that fails the
// turn: a timeout, a 500, an oversized result and a spent call-budget all come
// back as an error the SDK feeds to the model as a tool result, exactly as a
// bad run_sql does. The turn continues and the model can try something else.
func (t *Tool) Execute(ctx context.Context, input string) (string, error) {
	if !t.calls.take() {
		return "", fmt.Errorf("this turn has reached its limit on MCP tool calls; answer from what you already have")
	}

	args, err := parseArgs(input)
	if err != nil {
		return "", fmt.Errorf("could not parse arguments for %s: %w", t.name, err)
	}

	// Bound this one call. The guard's own dial/handshake timeouts still apply
	// per connection; this deadline is what stops a server that accepts and then
	// streams slowly from holding the turn open to the turn's wall-clock budget.
	callCtx := ctx
	if t.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	res, err := t.caller.CallTool(callCtx, t.url, t.transport, t.token, t.rawName, args, t.maxBytes)
	if err != nil {
		return "", fmt.Errorf("calling %s: %w", t.name, err)
	}

	// Metered here, after the round trip completed and before the result is
	// classified. A call the server answered is work done on the tenant's
	// behalf whether or not their tool reported a business error, and the
	// context the result occupies is paid for either way. A transport failure
	// records nothing, which is the same line run_sql draws: it meters a query
	// that ran, not one that could not.
	t.meter.RecordMCPCall(ctx, tenantctx.CompanyID(ctx), tenantctx.ThreadID(ctx), t.serverID, t.rawName)
	if res.IsError {
		// The tenant tool's own business error. Returned as a result, not a Go
		// error, so the model reads it and self-corrects — but marked, so it does
		// not read the message as a successful answer.
		text := strings.TrimSpace(res.Text)
		if text == "" {
			text = "the tool reported an error but returned no message"
		}
		return "[tool error] " + text, nil
	}
	return res.Text, nil
}

// parseArgs turns the model's JSON argument string into a map. An empty or
// whitespace input is a call with no arguments, which is legal — a tool that
// takes none still gets called — so it maps to an empty object rather than an
// error.
func parseArgs(input string) (map[string]any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// namespaced builds the model-facing name for a tool. mcp__<server>__<tool>,
// each part sanitised to the characters a tool name may hold, and the whole
// clipped to the provider ceiling. The server slug is for a human reading the
// name or the thread; the audit row's server id is the authoritative link.
func namespaced(serverName, toolName string) string {
	name := NamePrefix + slug(serverName) + "__" + slug(toolName)
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	return name
}

// slug reduces a name to [a-z0-9_-], the safe subset of the tool-name grammar,
// lower-cased so two spellings of one server do not read as two. An empty
// result (a name that was all punctuation) becomes "x" so the namespaced form
// is never left with a dangling separator.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == ' ':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return "x"
	}
	return out
}

// paramsFromSchema converts a tool's JSON Schema into the SDK's parameter map.
//
// The MCP input schema is a standard object schema — type/properties/required —
// and the SDK builds its wire tool definition from a flat map of property specs,
// exactly as the hand-written tools do. Nested object properties are carried by
// their declared type rather than expanded: the model gets the shape it needs
// for the common flat-argument tool, and a deeply nested schema degrades to
// "this is an object" rather than failing to register.
func paramsFromSchema(raw json.RawMessage) map[string]interfaces.ParameterSpec {
	out := map[string]interfaces.ParameterSpec{}
	if len(raw) == 0 {
		return out
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return out
	}
	required := map[string]bool{}
	for _, r := range schema.Required {
		required[r] = true
	}
	for name, propRaw := range schema.Properties {
		spec := specFromSchema(propRaw)
		spec.Required = required[name]
		out[name] = spec
	}
	return out
}

// specFromSchema reads one property's schema into a ParameterSpec. It carries
// type, description, enum, default and (for arrays) the item type. Required is
// set by the caller from the parent's required list, which is where JSON Schema
// keeps it.
func specFromSchema(raw json.RawMessage) interfaces.ParameterSpec {
	var s struct {
		Type        any             `json:"type"`
		Description string          `json:"description"`
		Enum        []any           `json:"enum"`
		Default     any             `json:"default"`
		Items       json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(raw, &s)
	spec := interfaces.ParameterSpec{
		Type:        s.Type,
		Description: s.Description,
		Default:     s.Default,
		Enum:        s.Enum,
	}
	if len(s.Items) > 0 {
		item := specFromSchema(s.Items)
		spec.Items = &item
	}
	return spec
}
