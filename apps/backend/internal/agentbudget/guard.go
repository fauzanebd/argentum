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

	out, err := g.Tool.Execute(ctx, args)
	tr.Observe(name, out, err)
	return out, err
}
