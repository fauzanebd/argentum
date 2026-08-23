package agentbudget

import (
	"context"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// Guard wraps a tool so every execution passes the turn's budget first and
// reports its result back to the tracker afterwards.
//
// Wrapping the tools is what makes the budget enforceable without forking
// agent-sdk-go: the tool boundary is the only place inside the provider's
// tool-calling loop that this codebase owns. It is also the only place where
// a message can be handed to the model mid-loop — a refused tool call comes
// back as an ordinary tool result, which the model reads and acts on, where
// an error would be swallowed by the provider and never reach it.
func Guard(t interfaces.Tool) interfaces.Tool { return &guarded{Tool: t} }

// GuardAll wraps every tool in the registry.
func GuardAll(tools []interfaces.Tool) []interfaces.Tool {
	out := make([]interfaces.Tool, len(tools))
	for i, t := range tools {
		out[i] = Guard(t)
	}
	return out
}

type guarded struct {
	interfaces.Tool
}

// Unwrap exposes the wrapped tool. It embeds interfaces.Tool, which hides any
// method the interface does not declare — so a decorator further out that wants
// an optional capability of the underlying tool (the audit log reading an MCP
// tool's server id, T-M2) has to walk in past this wrapper rather than rely on
// promotion. errors.Unwrap-shaped on purpose.
func (g *guarded) Unwrap() interfaces.Tool { return g.Tool }

func (g *guarded) Run(ctx context.Context, input string) (string, error) {
	return g.Execute(ctx, input)
}

func (g *guarded) Execute(ctx context.Context, args string) (string, error) {
	tr := FromContext(ctx)
	name := g.Tool.Name()

	if refusal, blocked := tr.Begin(ctx, name); blocked {
		snap := tr.Snapshot()
		logrus.WithFields(logrus.Fields{
			"company_id": tenantctx.CompanyID(ctx),
			"thread_id":  tenantctx.ThreadID(ctx),
			"tool":       name,
			"reason":     snap.Reason,
			"tool_calls": snap.ToolCalls,
			"elapsed_ms": snap.Elapsed.Milliseconds(),
		}).Warn("agent budget exhausted; tool call refused")
		return refusal, nil
	}

	// A deliverable call that runs on a turn already marked exhausted came out
	// of the reserve. Logged because it is the one path where a spent budget
	// still executes a tool, and "did the reserve fire?" is the first question
	// asked of a report turn that finished without a file.
	if snap := tr.Snapshot(); snap.Exhausted && IsDeliverableTool(name) {
		logrus.WithFields(logrus.Fields{
			"company_id": tenantctx.CompanyID(ctx),
			"thread_id":  tenantctx.ThreadID(ctx),
			"tool":       name,
			"reason":     snap.Reason,
			"tool_calls": snap.ToolCalls,
		}).Info("agent budget exhausted; reserved deliverable call allowed")
	}

	out, err := g.Tool.Execute(ctx, args)
	tr.Observe(name, out, err)

	// The repeat-guard, and it sits here for the reason this file's own comment
	// gives: the tool boundary is the only place inside the provider's loop
	// where a message can be handed to the model mid-turn. A loop that has to
	// be broken has to be broken from here.
	if refusal, looped := tr.NoteOutcome(name, args, out, err); looped {
		logrus.WithFields(logrus.Fields{
			"company_id": tenantctx.CompanyID(ctx),
			"thread_id":  tenantctx.ThreadID(ctx),
			"tool":       name,
		}).Warn("the same tool call failed the same way twice; ending the tool loop")
		return refusal, nil
	}
	return out, err
}
