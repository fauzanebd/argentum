package llmusage

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Serving is what the provider said it actually ran, as opposed to what we
// asked it to run.
//
// Why this exists (T-Q15): every quality number this project has published
// names a model string and no revision — `moonshotai/kimi-k2.6`,
// `deepseek/deepseek-v3.2` — so none of them can be re-run as the same
// measurement. The 2026-08-18 rule-1 re-score put deepseek six cases below its
// 2026-08-14 number, outside the set's ±2 band, and resolving that to provider
// drift rather than to a regression took a worktree at the previous commit and
// six more cases of spend. It worked because that commit was ninety minutes
// old; against a baseline from a week earlier it would not have.
//
// An OpenAI-compatible gateway echoes the model it served on every SSE frame,
// and OpenRouter names the upstream provider beside it. Both are already
// passing through the tap in transport.go, so recording them costs one JSON
// field per frame and makes a score say which revision produced it.
type Serving struct {
	// Model is the identifier the provider echoed back. Usually the string we
	// asked for; a gateway that resolves an alias reports the resolution here.
	Model string `json:"model"`
	// Provider is the upstream OpenRouter routed to ("DeepInfra", "Fireworks").
	// Empty on gateways that do not name one, which is most of them — a
	// provider swap under a stable model id is the drift T-Q15 was written
	// about, so it is the field worth having even though it is often blank.
	Provider string `json:"provider,omitempty"`
}

// Empty reports whether the provider named nothing at all.
func (s Serving) Empty() bool {
	return strings.TrimSpace(s.Model) == "" && strings.TrimSpace(s.Provider) == ""
}

// String renders one serving the way a report line reads it.
func (s Serving) String() string {
	switch {
	case s.Model == "" && s.Provider == "":
		return "(unnamed)"
	case s.Provider == "":
		return s.Model
	case s.Model == "":
		return "via " + s.Provider
	}
	return s.Model + " via " + s.Provider
}

// ObservedServing is one distinct Serving and how many provider responses
// carried it. The count is what tells a one-off route from the route that
// answered the whole run.
type ObservedServing struct {
	Serving
	Responses int `json:"responses"`
}

// ServingSink collects the distinct servings seen across however many calls
// the holder scopes it to. The eval harness scopes one to a whole model run,
// so a set scored while the gateway silently re-routed shows two rows instead
// of one — which is the finding, not a nuisance.
//
// Unlike Collector, a sink is optional and is installed by the *caller* rather
// than by MeteredLLM: nothing in the product needs this, and a per-turn sink
// on every production request would be a map allocation per turn to answer a
// question only a scoring run asks.
type ServingSink struct {
	mu   sync.Mutex
	seen map[Serving]int
}

// Observe records one provider response's self-identification.
func (s *ServingSink) Observe(v Serving) {
	if s == nil || v.Empty() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[Serving]int{}
	}
	s.seen[v]++
}

// Observed returns the distinct servings, most responses first, ties broken by
// model then provider so the output is stable enough to diff between runs.
func (s *ServingSink) Observed() []ObservedServing {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ObservedServing, 0, len(s.seen))
	for v, n := range s.seen {
		out = append(out, ObservedServing{Serving: v, Responses: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Responses != out[j].Responses {
			return out[i].Responses > out[j].Responses
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

type servingSinkKey struct{}

// WithServingSink returns a context carrying the sink. Every HTTP request made
// under that context reports what answered it.
func WithServingSink(ctx context.Context, s *ServingSink) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, servingSinkKey{}, s)
}

// ServingSinkFrom returns the sink installed by WithServingSink, or nil.
func ServingSinkFrom(ctx context.Context) *ServingSink {
	s, _ := ctx.Value(servingSinkKey{}).(*ServingSink)
	return s
}
