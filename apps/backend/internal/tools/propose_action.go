package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// ActionProposer is the narrow contract propose_action needs (T-10). Declared
// here, not in internal/app, to avoid an import cycle: internal/app already
// depends on internal/tools. *app.ActionService satisfies it.
//
// The interface has one method and it is Propose, not Execute. The tool cannot
// carry an action out — only the approval endpoint can (T-11) — so the capability
// to execute is not on the surface the agent can reach, rather than merely
// declined at runtime.
type ActionProposer interface {
	ProposeAction(ctx context.Context, in ProposeActionInput) (*ProposeActionResult, error)
}

// ProposeActionInput is the action kind and its parameters. Tenant, thread and
// message come from the context the turn set, exactly as the audit decorator
// reads them — the tool does not restate what the turn already knows.
type ProposeActionInput struct {
	Kind   string
	Params json.RawMessage
}

// ProposeActionResult is what the tool surfaces back to the agent, and through it
// to the user: the proposal's id, its state, and a sentence to relay.
type ProposeActionResult struct {
	InvocationID     string `json:"invocation_id"`
	ActionKind       string `json:"action_kind"`
	Status           string `json:"status"`
	RequiresApproval bool   `json:"requires_approval"`
	Description      string `json:"description"`
	Message          string `json:"message"`
}

// ProposeActionTool lets the agent propose a write-capable action for the current
// tenant (T-10). It never performs the action: it records a proposal and returns
// the id, and a human approves or rejects it from the dashboard. Registered
// unconditionally like the metric tools — a nil proposer still yields the tool's
// name for the allowlist and reports "not configured" if ever executed.
type ProposeActionTool struct {
	proposer ActionProposer
}

func NewProposeActionTool(proposer ActionProposer) *ProposeActionTool {
	return &ProposeActionTool{proposer: proposer}
}

func (t *ProposeActionTool) Name() string { return "propose_action" }

func (t *ProposeActionTool) Description() string {
	return "Propose a write-capable action — one that changes something outside Argentum, such as sending a message. " +
		"This tool does NOT perform the action: it records a proposal that a human must approve from the dashboard before anything happens. " +
		"Use it when the user has clearly asked for something to be done (not just answered), and only for an action kind the workspace has enabled. " +
		"The kinds this workspace has enabled, the params each one takes, and any names they may reference are listed under 'Actions this workspace has enabled' in the turn's system context. " +
		"Returns the proposal id and tells the user their approval is needed. If the requested action is not available, say so plainly rather than trying to do it another way."
}

func (t *ProposeActionTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"action_kind": {
			Type:        "string",
			Description: "The kind of action to propose. Must be one of the kinds listed in the turn's system context; if none is listed, no action is available and you should say so rather than guess.",
			Required:    true,
		},
		"params": {
			Type: "object",
			// The shapes used to be enumerated here — "for send_message:
			// channel, target_ref, body" — which is one tool description shared
			// by every tenant, so it could name one kind's parameters and never
			// the tenant's own endpoint names. The contract now travels with the
			// catalog, from each action's own Usage().
			Description: "The action's parameters, as a JSON object. Its shape depends on the action_kind and is given beside that kind in the turn's system context. Include everything the action needs.",
			Required:    true,
		},
	}
}

func (t *ProposeActionTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *ProposeActionTool) Execute(ctx context.Context, args string) (string, error) {
	if t.proposer == nil {
		return "", fmt.Errorf("actions are not configured on this deployment")
	}
	var params struct {
		Kind   string          `json:"action_kind"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if tenantctx.CompanyID(ctx) == "" {
		return "", fmt.Errorf("no tenant in context")
	}

	res, err := t.proposer.ProposeAction(ctx, ProposeActionInput{
		Kind:   params.Kind,
		Params: params.Params,
	})
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(res)
	return string(out), nil
}
