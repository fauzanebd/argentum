// Package agentscope carries which roster agent a turn is running as, and what
// that agent is allowed to reach.
//
// Why it exists (ticket T-S2). An agent is persona + tools + sources. The
// persona reaches the model through the system prompt and the tool allowlist
// through the factory, but the *source* allowlist has to be enforced inside the
// tools — a persona that says "only use the finance database" is a wish, and
// the choke point where a source is actually chosen is tools.ResolveSource,
// four packages away from anything that knows what an agent is.
//
// So the scope rides the context, exactly as agentbudget.Tracker does and for
// exactly the same reason: it is how one per-turn constraint reaches seven
// tools without changing seven signatures. ChatRunner installs it beside the
// budget tracker at the top of a turn.
//
// It is also what puts an agent id on the rows a turn writes. The audit
// decorator and the usage recorder both run deep inside a tool call with
// nothing but a context, and "which agent ran this / spent this" is the
// question the whole roster exists to make answerable.
package agentscope

import (
	"context"
	"slices"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Scope is one turn's agent identity and reach.
//
// The zero value is a turn with no roster agent — the eval harness, a company
// whose roster failed to seed, a task queued before T-S2 shipped. Every method
// is nil- and zero-safe and answers "unrestricted", because that is what the
// product did before this package existed and a turn must not become unable to
// query anything because a lookup missed.
type Scope struct {
	// AgentID is the roster row this turn runs as. Empty when there is none.
	AgentID string
	// Name is carried for logs only. Nothing branches on it.
	Name string
	// SourceIDs is the agent's connection allowlist. **Empty means every
	// source the company owns** — the same rule domain.Agent.AllowsSource
	// states, and the reason a scoped agent whose only source was deleted
	// widens rather than breaks.
	SourceIDs []string
}

type scopeKey struct{}

// WithScope carries s on ctx for the duration of one turn.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// FromContext returns the turn's scope, or the zero Scope when none was
// installed. A value rather than a pointer: there is nothing to mutate, and a
// nil pointer at every call site is a nil check at every call site.
func FromContext(ctx context.Context) Scope {
	s, _ := ctx.Value(scopeKey{}).(Scope)
	return s
}

// AgentID is the turn's agent, or "" when it is running unscoped. It is the
// value the audit log and the usage events record.
func AgentID(ctx context.Context) string { return FromContext(ctx).AgentID }

// AllowsSource reports whether this turn's agent may reach a connection.
// Empty allowlist means every source; see the field comment.
func (s Scope) AllowsSource(connectionID string) bool {
	return len(s.SourceIDs) == 0 || slices.Contains(s.SourceIDs, connectionID)
}

// FilterSources narrows a company's connections to what this agent may see.
//
// One function, three call sites — tools.ResolveSource, ListSourcesTool, and
// the catalog ChatRunner injects into the message. They have to agree: an agent
// told about a source its tools will then refuse is the most confusing failure
// available here, and it is one no tool-level test catches.
//
// The unrestricted case returns the input slice untouched rather than a copy.
// It is the common path and nothing downstream writes to it.
func (s Scope) FilterSources(conns []*domain.DBConnection) []*domain.DBConnection {
	if len(s.SourceIDs) == 0 || len(conns) == 0 {
		return conns
	}
	out := make([]*domain.DBConnection, 0, len(conns))
	for _, c := range conns {
		if s.AllowsSource(c.ID) {
			out = append(out, c)
		}
	}
	return out
}
