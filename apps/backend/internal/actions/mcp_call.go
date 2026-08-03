package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	adaptersmcp "github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
)

// MCPTarget is one write-capable tool on one of a company's MCP servers,
// resolved to everything a call needs. The token arrives decrypted, as
// http_action's endpoint does: the one moment a credential is in the clear is
// the moment a request is built from it.
type MCPTarget struct {
	ServerID   string
	ServerName string
	// ToolName is the tenant's own name for the tool, which is what goes on the
	// wire. The namespaced name the agent proposed is a label for our side.
	ToolName  string
	URL       string
	Transport domain.MCPTransport
	Token     string
}

// MCPCallStore resolves a namespaced tool name for the company on ctx, and
// lists the names that exist so a turn can be told them.
//
// Declared here and implemented in internal/app, where the repository and the
// cipher live — the same seam EndpointStore uses, for the same reason. Both
// methods re-check the three gates at the moment they are asked: approved, not
// read-only, not drifted, on an enabled server. A proposal can sit for a day
// (T-10's TTL), and the answer to "may this run?" is the one that is true when
// the human approves, not when the agent asked.
type MCPCallStore interface {
	FindWriteTool(ctx context.Context, namespacedName string) (MCPTarget, error)
	ListWriteToolNames(ctx context.Context) ([]string, error)
}

// MCPCaller is the guarded MCP client, narrowed to the one call this action
// makes. Same shape as mcptools.Caller and satisfied by the same client, so the
// egress guard cannot be skipped by reaching this path instead of that one.
type MCPCaller interface {
	CallTool(ctx context.Context, url string, transport domain.MCPTransport, token, toolName string,
		args map[string]any, maxBytes int) (adaptersmcp.CallResult, error)
}

// mcpCallParams is what the agent proposes: which tool, and the arguments
// exactly as they will be sent.
type mcpCallParams struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

// MCPCall runs a write-capable tool on a tenant's own MCP server, after a human
// has approved it (T-M4).
//
// Locked decision 2 keeps the MCP source read-only, and the reason a customer
// registers their ticketing system is to have a ticket created. This is that,
// without becoming the side door around it: a write tool is never called from
// the tool-calling loop. The agent's call *proposes*; this action is what runs,
// once, from T-10's state machine, after somebody with standing said yes.
type MCPCall struct {
	store    MCPCallStore
	caller   MCPCaller
	timeout  time.Duration
	maxBytes int
}

// NewMCPCall wires the action. A nil store or caller is a wiring error rather
// than a runtime branch, exactly as http_action treats its two: an action that
// cannot resolve a tool or cannot call one would accept proposals it could never
// carry out, and a human would approve them.
func NewMCPCall(store MCPCallStore, caller MCPCaller, timeout time.Duration, maxBytes int) *MCPCall {
	if store == nil || caller == nil {
		panic("actions: mcp_call requires a tool store and an MCP caller")
	}
	return &MCPCall{store: store, caller: caller, timeout: timeout, maxBytes: maxBytes}
}

// MCPCallKind is the action kind, exported because the tool that proposes it
// has to name it and a second spelling would be a silent no-op.
const MCPCallKind = "mcp_call"

func (a *MCPCall) Kind() string { return MCPCallKind }

func (a *MCPCall) parse(params json.RawMessage) (mcpCallParams, error) {
	var p mcpCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return p, fmt.Errorf("invalid parameters: %w", err)
	}
	p.Tool = strings.TrimSpace(p.Tool)
	if p.Tool == "" {
		return p, fmt.Errorf("tool is required — the name of an approved write tool on a registered MCP server")
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	return p, nil
}

// Validate checks the shape only. Like http_action's, it runs before the
// proposal is stored and cannot see the tenant, so whether the named tool exists
// — and whether it is still approved, still a write, still undrifted — is
// Execute's, where the company is known.
func (a *MCPCall) Validate(params json.RawMessage) error {
	_, err := a.parse(params)
	return err
}

func (a *MCPCall) Usage() string {
	return `run a write-capable tool on one of the MCP servers this workspace registered. ` +
		`params: {"tool": "<a namespaced write tool name>", "arguments": {…}}. ` +
		`It is proposed, not run: a human approves it first, and the arguments they see are the ones sent.`
}

// TurnOptions lists the write tools this company has, so a turn is told which
// names exist rather than inventing one. Read-only tools are not here — those
// the agent calls directly and needs no proposal for.
func (a *MCPCall) TurnOptions(ctx context.Context) ([]string, error) {
	return a.store.ListWriteToolNames(ctx)
}

// Describe renders the approval sentence: which tool, and the arguments as they
// will be sent. Not a summary and not a truncation — the ticket's rule is that
// an approval is only meaningful against the literal payload, so the whole
// argument object goes on the card.
//
// It needs no lookup: the namespaced name carries the server's slug
// (mcp__<server>__<tool>), which is what a human reading the card recognises,
// and Describe is ctx-free so a malformed proposal is refused before anyone is
// asked to read it.
func (a *MCPCall) Describe(params json.RawMessage) (string, error) {
	p, err := a.parse(params)
	if err != nil {
		return "", err
	}
	if len(p.Arguments) == 0 {
		return fmt.Sprintf("Run the MCP tool %q with no arguments", p.Tool), nil
	}
	// Marshalled from the same map Execute sends, so the sentence cannot drift
	// from the payload. Sorted by encoding/json's own key ordering, which is
	// stable, so two readings of one proposal read identically.
	args, err := json.Marshal(p.Arguments)
	if err != nil {
		return "", fmt.Errorf("render arguments: %w", err)
	}
	return fmt.Sprintf("Run the MCP tool %q with %s", p.Tool, string(args)), nil
}

// Execute calls the tenant's server, post-approval.
//
// The three gates are re-read here rather than trusted from propose time. A
// proposal is approvable for a day: a tool an admin un-approved, re-classified
// as a write by mistake and corrected, or whose description drifted since, must
// not run because it was legal yesterday.
func (a *MCPCall) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	p, err := a.parse(params)
	if err != nil {
		return nil, err
	}

	target, err := a.store.FindWriteTool(ctx, p.Tool)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, fmt.Errorf("%w: no approved write tool named %q is available on this workspace's MCP servers; "+
				"an admin may have un-approved it, re-classified it, or its description may have changed since it was approved",
				domain.ErrInvalidInput, p.Tool)
		}
		return nil, fmt.Errorf("look up MCP tool: %w", err)
	}

	callCtx := ctx
	if a.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, a.timeout)
		defer cancel()
	}

	res, err := a.caller.CallTool(callCtx, target.URL, target.Transport, target.Token, target.ToolName, p.Arguments, a.maxBytes)
	if err != nil {
		return nil, fmt.Errorf("call %s on %s: %w", target.ToolName, target.ServerName, err)
	}

	out, _ := json.Marshal(map[string]any{
		"tool":        p.Tool,
		"server_id":   target.ServerID,
		"server_name": target.ServerName,
		"tool_name":   target.ToolName,
		"is_error":    res.IsError,
		"result":      res.Text,
	})
	// A tool that answers unhappily is not a failed execution, the same line
	// http_action draws on a 4xx: the call was made, and the ledger records what
	// came back. A failure is the guard refusing, the network dropping, or the
	// tool no longer being one we may run.
	return out, nil
}
