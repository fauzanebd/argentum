// Package llmusage taps token usage out of OpenAI-compatible streaming
// responses at the HTTP layer.
//
// Why this exists (finding C-2 / ticket T-02c): agent-sdk-go's OpenAI client
// only forwards `chunk.Usage` into a StreamEvent from GenerateStream — the
// no-tools path (pkg/llm/openai/streaming.go:212). GenerateWithToolsStream, the
// path every agent turn actually takes, sets `stream_options.include_usage` on
// the request and then drops the usage chunk on the floor. So the provider
// reports the tokens, the SDK reads them, and nobody records them: every
// streaming agent turn on an OpenAI-interface provider was free of charge as
// far as Argentum knew.
//
// Rather than fork the SDK, we read the usage off the wire. A collector is
// installed into the request context by app.MeteredLLM before it calls the
// stream method; the RoundTripper below finds it on each HTTP request the SDK
// makes — including every iteration of the tool-calling loop — and accumulates
// what the provider reported. Anthropic's client emits usage in stream event
// metadata (that is what commit 74f5419 built cache billing on) and is left
// alone: it never gets a collector-bearing transport, so nothing changes there.
package llmusage

import (
	"context"
	"sync"
)

// Usage is the provider-reported token count for one LLM request, normalised
// to Anthropic-native semantics — InputTokens EXCLUDES cache tokens, because
// that is what app.UsageService.RecordLLM prices (cache reads at 0.10x, cache
// writes at 1.25x). OpenAI's `prompt_tokens` includes cached tokens, so the
// parser subtracts them; see normalize in transport.go.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// Empty reports whether nothing was counted.
func (u Usage) Empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0
}

// Collector accumulates usage across every HTTP request made during one
// logical LLM call. One agent turn is one GenerateWithToolsStream call, which
// issues one HTTP request per tool-calling iteration — all of them land here.
type Collector struct {
	mu     sync.Mutex
	usage  Usage
	events int
}

// Add accumulates one provider usage report. Events counts the reports, which
// is the "usage events per turn" signal: zero means the provider or the SDK
// gave us nothing and the turn is unbilled.
func (c *Collector) Add(u Usage) {
	if c == nil || u.Empty() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usage.InputTokens += u.InputTokens
	c.usage.OutputTokens += u.OutputTokens
	c.usage.CacheCreationInputTokens += u.CacheCreationInputTokens
	c.usage.CacheReadInputTokens += u.CacheReadInputTokens
	c.events++
}

// Snapshot returns the accumulated usage and how many provider reports made it up.
func (c *Collector) Snapshot() (Usage, int) {
	if c == nil {
		return Usage{}, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage, c.events
}

type collectorKey struct{}

// WithCollector returns a context carrying a fresh Collector, plus the
// collector itself. Call it once per logical LLM call.
func WithCollector(ctx context.Context) (context.Context, *Collector) {
	c := &Collector{}
	return context.WithValue(ctx, collectorKey{}, c), c
}

// CollectorFrom returns the collector installed by WithCollector, or nil.
func CollectorFrom(ctx context.Context) *Collector {
	c, _ := ctx.Value(collectorKey{}).(*Collector)
	return c
}
