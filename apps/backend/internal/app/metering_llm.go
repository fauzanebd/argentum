package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/llmusage"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// MeteredLLM wraps an interfaces.LLM and records token usage to a
// UsageService for every call. The wrapped LLM remains a drop-in replacement
// for the underlying client — the agent-sdk doesn't need to know.
type MeteredLLM struct {
	inner interfaces.LLM
	model string // real model string (e.g. "deepseek/deepseek-v3.2"); inner.Name() returns provider tag only.
	usage *UsageService
}

// NewMeteredLLM returns an LLM wrapper that records token usage. model is the
// real model string used for pricing lookup — inner.Name() returns the
// provider tag ("openai", "anthropic"), not the model.
func NewMeteredLLM(inner interfaces.LLM, model string, usage *UsageService) *MeteredLLM {
	return &MeteredLLM{inner: inner, model: strings.TrimSpace(model), usage: usage}
}

// Model returns the real model string this wrapper bills against.
func (m *MeteredLLM) Model() string { return m.model }

func (m *MeteredLLM) Generate(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (string, error) {
	resp, err := m.inner.GenerateDetailed(ctx, prompt, opts...)
	if err != nil {
		return "", err
	}
	m.record(ctx, resp.Usage)
	return resp.Content, nil
}

func (m *MeteredLLM) GenerateWithTools(ctx context.Context, prompt string, tools []interfaces.Tool, opts ...interfaces.GenerateOption) (string, error) {
	resp, err := m.inner.GenerateWithToolsDetailed(ctx, prompt, tools, opts...)
	if err != nil {
		return "", err
	}
	m.record(ctx, resp.Usage)
	return resp.Content, nil
}

func (m *MeteredLLM) GenerateDetailed(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	resp, err := m.inner.GenerateDetailed(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}
	m.record(ctx, resp.Usage)
	return resp, nil
}

func (m *MeteredLLM) GenerateWithToolsDetailed(ctx context.Context, prompt string, tools []interfaces.Tool, opts ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	resp, err := m.inner.GenerateWithToolsDetailed(ctx, prompt, tools, opts...)
	if err != nil {
		return nil, err
	}
	m.record(ctx, resp.Usage)
	return resp, nil
}

func (m *MeteredLLM) Name() string            { return m.inner.Name() }
func (m *MeteredLLM) SupportsStreaming() bool { return m.inner.SupportsStreaming() }

// GenerateStream wraps the inner StreamingLLM and records token usage when the
// stream emits it in Metadata["usage"]. We prepend interfaces.WithReasoning so
// agent-sdk-go's OpenAI client sets stream_options.include_usage:true even for
// non-reasoning models — without it, OpenRouter / non-reasoning OpenAI routes
// emit no usage chunk and we'd record zero tokens for the whole turn.
//
// The context also carries a llmusage.Collector, which is how the HTTP-level
// tap in internal/llmusage reports usage the SDK requested but never forwarded
// — see the wrapStream comment and finding C-2.
func (m *MeteredLLM) GenerateStream(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	streamingLLM, ok := m.inner.(interfaces.StreamingLLM)
	if !ok {
		return nil, fmt.Errorf("inner LLM '%s' does not support streaming", m.inner.Name())
	}
	ctx, tap := llmusage.WithCollector(ctx)
	inner, err := streamingLLM.GenerateStream(ctx, prompt, withForcedUsage(opts)...)
	if err != nil {
		return nil, err
	}
	return m.wrapStream(ctx, inner, tap), nil
}

// GenerateWithToolsStream wraps the inner StreamingLLM the same way as
// GenerateStream. This is the path every agent turn takes, and the one where
// agent-sdk-go drops the provider's usage chunk — see wrapStream.
func (m *MeteredLLM) GenerateWithToolsStream(ctx context.Context, prompt string, tools []interfaces.Tool, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	streamingLLM, ok := m.inner.(interfaces.StreamingLLM)
	if !ok {
		return nil, fmt.Errorf("inner LLM '%s' does not support streaming", m.inner.Name())
	}
	ctx, tap := llmusage.WithCollector(ctx)
	inner, err := streamingLLM.GenerateWithToolsStream(ctx, prompt, tools, withForcedUsage(opts)...)
	if err != nil {
		return nil, err
	}
	return m.wrapStream(ctx, inner, tap), nil
}

// withForcedUsage appends an option that flips LLMConfig.EnableReasoning on,
// which is the only knob the agent-sdk-go OpenAI client checks (besides the
// model being a reasoning model) before setting include_usage on the stream
// request. For non-reasoning models the SDK only logs a debug line — no
// reasoning_effort is sent because that's gated on isReasoningModel(c.Model).
func withForcedUsage(opts []interfaces.GenerateOption) []interfaces.GenerateOption {
	forced := func(o *interfaces.GenerateOptions) {
		if o.LLMConfig == nil {
			o.LLMConfig = &interfaces.LLMConfig{}
		}
		o.LLMConfig.EnableReasoning = true
	}
	return append(opts, forced)
}

// wrapStream forwards events from inner to out and records the final usage
// once the stream closes. Usage is accumulated across all chunks because
// providers may emit it in pieces (tool-call iterations each emit their own).
//
// Two sources, in priority order:
//
//  1. Stream event metadata. Anthropic's SSE client populates it (including
//     cache_creation/cache_read), which is what commit 74f5419 bills on.
//  2. The HTTP tap (internal/llmusage). agent-sdk-go's OpenAI client requests
//     include_usage but only forwards the usage chunk from its no-tools
//     GenerateStream path; GenerateWithToolsStream — every agent turn —
//     silently discards it, which is finding C-2: a full multi-step turn
//     recorded zero usage for the primary model.
//
// Metadata wins when present so the Anthropic path is byte-for-byte unchanged.
// Neither source producing anything is a billing hole, not a curiosity: it is
// logged at Warn and counted, because silence is exactly what let C-2 survive
// to production.
func (m *MeteredLLM) wrapStream(ctx context.Context, inner <-chan interfaces.StreamEvent, tap *llmusage.Collector) <-chan interfaces.StreamEvent {
	out := make(chan interfaces.StreamEvent, 16)
	go func() {
		defer close(out)
		var agg interfaces.TokenUsage
		for evt := range inner {
			if u, ok := extractUsage(evt.Metadata); ok {
				agg.InputTokens += u.InputTokens
				agg.OutputTokens += u.OutputTokens
				agg.CacheCreationInputTokens += u.CacheCreationInputTokens
				agg.CacheReadInputTokens += u.CacheReadInputTokens
			}
			out <- evt
		}
		fromMetadata := agg.InputTokens > 0 || agg.OutputTokens > 0 ||
			agg.CacheCreationInputTokens > 0 || agg.CacheReadInputTokens > 0
		if fromMetadata {
			metrics.Default().RecordLLMStreamTurn(1)
			m.record(ctx, &agg)
			return
		}
		tapped, events := tap.Snapshot()
		if events > 0 {
			metrics.Default().RecordLLMStreamTurn(events)
			m.record(ctx, &interfaces.TokenUsage{
				InputTokens:              tapped.InputTokens,
				OutputTokens:             tapped.OutputTokens,
				CacheCreationInputTokens: tapped.CacheCreationInputTokens,
				CacheReadInputTokens:     tapped.CacheReadInputTokens,
			})
			return
		}
		metrics.Default().RecordLLMStreamTurn(0)
		logrus.WithFields(logrus.Fields{
			"company_id": tenantctx.CompanyID(ctx),
			"thread_id":  tenantctx.ThreadID(ctx),
			"model":      m.model,
			"provider":   m.inner.Name(),
		}).Warn("streaming turn completed with no usage reported by provider or HTTP tap — this turn is unbilled")
	}()
	return out
}

// extractUsage pulls token counts out of a stream event's Metadata["usage"]
// payload. Returns ok=false when usage is absent. Handles two shapes:
//   - map[string]interface{} — what agent-sdk-go's OpenAI streaming sets
//     (keys: prompt_tokens/completion_tokens or input_tokens/output_tokens).
//   - a typed Go struct — what agent-sdk-go's Anthropic SSE sets directly
//     (the Anthropic Usage struct includes cache_*_input_tokens fields).
//
// The struct case is reflected through JSON so we don't import provider
// internals.
func extractUsage(md map[string]interface{}) (interfaces.TokenUsage, bool) {
	raw, exists := md["usage"]
	if !exists {
		return interfaces.TokenUsage{}, false
	}
	if u, ok := raw.(map[string]interface{}); ok {
		return usageFromMap(u)
	}
	// Typed-struct path: round-trip through JSON. Anthropic SDK Usage struct
	// uses input_tokens / output_tokens / cache_creation_input_tokens /
	// cache_read_input_tokens JSON tags.
	if v := reflect.ValueOf(raw); v.IsValid() && (v.Kind() == reflect.Struct ||
		(v.Kind() == reflect.Pointer && v.Elem().Kind() == reflect.Struct)) {
		b, err := json.Marshal(raw)
		if err != nil {
			return interfaces.TokenUsage{}, false
		}
		var m map[string]interface{}
		if err := json.Unmarshal(b, &m); err != nil {
			return interfaces.TokenUsage{}, false
		}
		return usageFromMap(m)
	}
	return interfaces.TokenUsage{}, false
}

func usageFromMap(u map[string]interface{}) (interfaces.TokenUsage, bool) {
	var out interfaces.TokenUsage
	out.InputTokens = toInt(u["prompt_tokens"])
	out.OutputTokens = toInt(u["completion_tokens"])
	if out.InputTokens == 0 && out.OutputTokens == 0 {
		out.InputTokens = toInt(u["input_tokens"])
		out.OutputTokens = toInt(u["output_tokens"])
	}
	out.CacheCreationInputTokens = toInt(u["cache_creation_input_tokens"])
	out.CacheReadInputTokens = toInt(u["cache_read_input_tokens"])
	if out.InputTokens == 0 && out.OutputTokens == 0 &&
		out.CacheCreationInputTokens == 0 && out.CacheReadInputTokens == 0 {
		return out, false
	}
	return out, true
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func (m *MeteredLLM) record(ctx context.Context, usage *interfaces.TokenUsage) {
	if usage == nil || m.usage == nil {
		return
	}
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return
	}
	threadID := tenantctx.ThreadID(ctx)
	model := m.model
	if model == "" {
		model = m.inner.Name()
	}
	m.usage.RecordLLM(ctx, companyID, threadID, "", model,
		usage.InputTokens, usage.OutputTokens,
		usage.CacheCreationInputTokens, usage.CacheReadInputTokens)
}
