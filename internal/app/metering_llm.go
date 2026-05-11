package app

import (
	"context"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// MeteredLLM wraps an interfaces.LLM and records token usage to a
// UsageService for every call. The wrapped LLM remains a drop-in replacement
// for the underlying client — the agent-sdk doesn't need to know.
type MeteredLLM struct {
	inner interfaces.LLM
	usage *UsageService
}

// NewMeteredLLM returns an LLM wrapper that records token usage to the
// supplied service. The company / thread IDs come from tenantctx, which is
// populated by the chat service before calling Agent.Run.
func NewMeteredLLM(inner interfaces.LLM, usage *UsageService) *MeteredLLM {
	return &MeteredLLM{inner: inner, usage: usage}
}

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

func (m *MeteredLLM) Name() string             { return m.inner.Name() }
func (m *MeteredLLM) SupportsStreaming() bool { return m.inner.SupportsStreaming() }

// GenerateStream wraps the inner StreamingLLM and records token usage when the
// stream emits it in Metadata["usage"] (OpenAI-compatible providers set
// stream_options.include_usage:true and surface prompt/completion token counts
// just before the final stop event).
func (m *MeteredLLM) GenerateStream(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	streamingLLM, ok := m.inner.(interfaces.StreamingLLM)
	if !ok {
		return nil, fmt.Errorf("inner LLM '%s' does not support streaming", m.inner.Name())
	}
	inner, err := streamingLLM.GenerateStream(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}
	return m.wrapStream(ctx, inner), nil
}

// GenerateWithToolsStream wraps the inner StreamingLLM the same way as
// GenerateStream.
func (m *MeteredLLM) GenerateWithToolsStream(ctx context.Context, prompt string, tools []interfaces.Tool, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	streamingLLM, ok := m.inner.(interfaces.StreamingLLM)
	if !ok {
		return nil, fmt.Errorf("inner LLM '%s' does not support streaming", m.inner.Name())
	}
	inner, err := streamingLLM.GenerateWithToolsStream(ctx, prompt, tools, opts...)
	if err != nil {
		return nil, err
	}
	return m.wrapStream(ctx, inner), nil
}

// wrapStream forwards events from inner to out and records the final usage
// once the stream closes. Usage is accumulated across all chunks because
// providers may emit it in pieces (tool-call iterations each emit their own).
func (m *MeteredLLM) wrapStream(ctx context.Context, inner <-chan interfaces.StreamEvent) <-chan interfaces.StreamEvent {
	out := make(chan interfaces.StreamEvent, 16)
	go func() {
		defer close(out)
		var totalIn, totalOut int
		for evt := range inner {
			if in, outTok, ok := extractUsage(evt.Metadata); ok {
				totalIn += in
				totalOut += outTok
			}
			out <- evt
		}
		if totalIn > 0 || totalOut > 0 {
			m.record(ctx, &interfaces.TokenUsage{InputTokens: totalIn, OutputTokens: totalOut})
		}
	}()
	return out
}

// extractUsage pulls prompt/completion token counts out of a stream event's
// Metadata["usage"] map. Returns ok=false when usage is absent or malformed.
func extractUsage(md map[string]interface{}) (in, out int, ok bool) {
	raw, exists := md["usage"]
	if !exists {
		return 0, 0, false
	}
	u, isMap := raw.(map[string]interface{})
	if !isMap {
		return 0, 0, false
	}
	in = toInt(u["prompt_tokens"])
	out = toInt(u["completion_tokens"])
	if in == 0 && out == 0 {
		// Some providers only emit input_tokens / output_tokens.
		in = toInt(u["input_tokens"])
		out = toInt(u["output_tokens"])
	}
	if in == 0 && out == 0 {
		return 0, 0, false
	}
	return in, out, true
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
	m.usage.RecordLLM(ctx, companyID, threadID, "", m.inner.Name(), usage.InputTokens, usage.OutputTokens)
}
