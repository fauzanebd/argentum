package tools

import (
	"context"
	"sync"
)

// turnSource remembers which connection this turn has already resolved, so a
// later tool call that omits source_id can continue against the same one
// instead of being sent back to the menu.
//
// The problem it solves is a retry loop, not an ergonomic wrinkle. On a tenant
// with two sources the agent calls get_schema, reads a schema, then calls
// a second data tool without source_id; ResolveSource refuses with a message
// naming both ids; the agent calls it again unchanged, three to five times,
// until the iteration budget ends the turn before create_dashboard is reached.
// Recorded as reproducible defect 2 in coverage/eval-sprint1.md §4 — two of
// three attempts on deepseek-v3.2, and again on moonshotai/kimi-k2.6 on
// 2026-08-14, so it is not one model's blind spot.
//
// A pointer holder rather than a context value per resolve: the value has to be
// written by a callee and read by the next one, and every tool call in a turn
// shares the one context the runner built.
type turnSource struct {
	mu sync.Mutex
	id string
}

type turnSourceKey struct{}

// WithTurnSource installs an empty turn-source memory on ctx. The chat runner
// calls it once per turn, beside WithScope.
//
// A context without one is not an error and not a special case: the memory
// stays nil, nothing is remembered, and ResolveSource behaves exactly as it did
// before this existed. That is what keeps every other caller — the report
// service, the MCP server, tests — on the old path until they opt in.
func WithTurnSource(ctx context.Context) context.Context {
	return context.WithValue(ctx, turnSourceKey{}, &turnSource{})
}

func turnSourceFrom(ctx context.Context) *turnSource {
	t, _ := ctx.Value(turnSourceKey{}).(*turnSource)
	return t
}

// rememberSource records the connection this turn resolved. Called on every
// successful resolve, including the explicit-id ones: an agent that named a
// source in call one has told us which world it is working in, and that is
// exactly the context call two is missing.
func rememberSource(ctx context.Context, id string) {
	t := turnSourceFrom(ctx)
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.id = id
}

// recalledSource returns the connection this turn already resolved, or "".
func recalledSource(ctx context.Context) string {
	t := turnSourceFrom(ctx)
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.id
}
