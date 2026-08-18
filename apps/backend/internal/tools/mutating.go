package tools

import (
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/docgen"
)

// Which tools change stored state, as a property of the tool (T-Q13).
//
// **Why this is not a list.** The check it feeds asks whether a turn that
// claimed to have changed something actually called anything that could. A
// constant somewhere else answering that question would be right on the day it
// was written and wrong the first time a tool was added — which is the `T-14`
// lesson this repo has already paid for once: a promise kept in a second place
// drifts from the first, and the drift is silent. The author of the next
// write-capable tool has to answer this question on the tool itself, and if
// they do not, MutatingNames simply does not list it and the check under-counts
// rather than mis-counting.
//
// **What counts as mutating.** Stored state a *tenant* can later observe: a
// dashboard, a scheduled task, a generated document, a proposed action. Not the
// audit row a read leaves behind, and not a cache — those are records of the
// turn, not things the reply could claim to have done for somebody.

// Mutator is implemented by a tool whose successful execution changes stored
// state. The method is on the concrete tool rather than in the registry so that
// adding a tool and forgetting this is a visible omission in one file.
type Mutator interface {
	Mutating() bool
}

func (t *CreateDashboardTool) Mutating() bool { return true }
func (t *UpdateDashboardTool) Mutating() bool { return true }
func (t *ScheduleTaskTool) Mutating() bool    { return true }

// Mutating is true: generate_document writes a file and a `documents` row, both
// of which a tenant sees and a reply can claim to have produced.
func (t *GenerateDocumentTool) Mutating() bool { return true }

// Mutating is true: propose_action writes a proposal row awaiting approval. It
// counts in the sense that matters here — "I've scheduled that message" is a
// claim about something now in the database — even though the action itself has
// not run.
func (t *ProposeActionTool) Mutating() bool { return true }

// IsMutating reports whether this tool changes stored state, walking through
// any decorators in the way.
//
// The unwrapping is the load-bearing part. Every tool the worker runs is
// wrapped twice — by the budget guard and by the audit recorder — and both
// embed interfaces.Tool, which hides methods the interface does not declare
// (`agentbudget/guard.go:36-41` says so in as many words). A check that asked
// the wrapper would find no mutating tools at all and would therefore never
// count anything, which is the worst possible failure for an instrument: silent
// and shaped exactly like good news.
func IsMutating(t interfaces.Tool) bool {
	for t != nil {
		if m, ok := t.(Mutator); ok {
			return m.Mutating()
		}
		u, ok := t.(interface{ Unwrap() interfaces.Tool })
		if !ok {
			return false
		}
		t = u.Unwrap()
	}
	return false
}

// MutatingNames is every tool name in this *release* that changes stored state.
//
// Built from the same Registry as AllNames and for the same reason: a list
// maintained beside it would be a second answer to a question that has one.
// Docs is supplied so `generate_document` is included on every deployment —
// the caller compares names against what a turn called, and a deployment
// without object storage simply never sees that name.
func MutatingNames() []string {
	var out []string
	for _, t := range Registry(RegistryDeps{Docs: &docgen.Service{}}) {
		if IsMutating(t) {
			out = append(out, t.Name())
		}
	}
	return out
}
