package mcptools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/tools"
)

// Proposer is the half of the action framework a write tool needs (T-M4).
// *app.ActionService satisfies it, and so does tools.ActionProposer — this is
// the same contract propose_action calls through, which is the point: a write
// tool is not a second write path, it is a nicer front door onto the one that
// already exists.
type Proposer interface {
	ProposeAction(ctx context.Context, in tools.ProposeActionInput) (*tools.ProposeActionResult, error)
}

// WriteTool is an approved, **not** read-only MCP tool, presented to the model
// as an ordinary tool that happens to require a human.
//
// The agent calls it exactly as it calls a read tool — same namespaced name,
// same argument schema. What it does is record a proposal for the `mcp_call`
// action and answer with the sentence that says so. Nothing here reaches the
// tenant's server: the call is made later, once, by ActionService.Execute after
// somebody approved it.
//
// Offering it as a tool rather than making the model construct a propose_action
// payload is deliberate. The 2026-08-02 gate watched four turns try to reach
// `http_action` through propose_action and one succeed, and the one that
// succeeded was the turn whose user message dictated the arguments. A model that
// can see `mcp__kirim_cepat__cancel_shipment` in its tool list, with the
// server's own schema on it, does not have to be told any of that.
type WriteTool struct {
	serverID   string
	serverName string
	rawName    string
	name       string
	desc       string
	params     map[string]interfaces.ParameterSpec

	proposer Proposer
}

var _ interfaces.Tool = (*WriteTool)(nil)

func (t *WriteTool) Name() string { return t.name }

// Description carries the tenant's own text plus the one fact that changes how
// the model should use it. Without the sentence, a model told only what the tool
// does will report the ticket as filed the moment the call returns.
func (t *WriteTool) Description() string {
	return t.desc + " — This tool changes something outside Argentum, so calling it proposes the change " +
		"and a person approves it. Tell the user it needs approval; do not report it as done."
}

func (t *WriteTool) Parameters() map[string]interfaces.ParameterSpec { return t.params }

// MCPServerID is what the audit decorator records, the same as a read tool's —
// so the proposal is one audited tool call, and the execution that follows is a
// second audited event from the action framework.
func (t *WriteTool) MCPServerID() string { return t.serverID }

func (t *WriteTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

// Execute records the proposal. It deliberately does not consume the per-turn
// MCP call budget: that cap bounds round trips to a tenant's server, and this
// makes none. What bounds a turn that proposes in a loop is T-16's tool-call
// budget, which counts this like any other call.
func (t *WriteTool) Execute(ctx context.Context, input string) (string, error) {
	if t.proposer == nil {
		return "", fmt.Errorf("%s needs approval to run and this deployment has no action framework configured", t.name)
	}

	args, err := parseArgs(input)
	if err != nil {
		return "", fmt.Errorf("could not parse arguments for %s: %w", t.name, err)
	}

	params, err := json.Marshal(map[string]any{"tool": t.name, "arguments": args})
	if err != nil {
		return "", fmt.Errorf("encode proposal for %s: %w", t.name, err)
	}

	res, err := t.proposer.ProposeAction(ctx, tools.ProposeActionInput{
		Kind:   actions.MCPCallKind,
		Params: params,
	})
	if err != nil {
		// Returned to the model as a tool result, like every other MCP failure:
		// "the mcp_call action is not enabled for this workspace" is something it
		// can relay to the user, and something the user can act on.
		return "", fmt.Errorf("proposing %s: %w", t.name, err)
	}
	return res.Message, nil
}
