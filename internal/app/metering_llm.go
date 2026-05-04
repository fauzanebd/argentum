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

// GenerateStream delegates to the inner StreamingLLM so the agent can use
// RunStream. Token usage is not recorded for streaming today (the channel
// events don't carry usage metadata).
func (m *MeteredLLM) GenerateStream(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	streamingLLM, ok := m.inner.(interfaces.StreamingLLM)
	if !ok {
		return nil, fmt.Errorf("inner LLM '%s' does not support streaming", m.inner.Name())
	}
	return streamingLLM.GenerateStream(ctx, prompt, opts...)
}

// GenerateWithToolsStream delegates to the inner StreamingLLM.
func (m *MeteredLLM) GenerateWithToolsStream(ctx context.Context, prompt string, tools []interfaces.Tool, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	streamingLLM, ok := m.inner.(interfaces.StreamingLLM)
	if !ok {
		return nil, fmt.Errorf("inner LLM '%s' does not support streaming", m.inner.Name())
	}
	return streamingLLM.GenerateWithToolsStream(ctx, prompt, tools, opts...)
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
	m.usage.RecordLLM(ctx, companyID, threadID, "", usage.InputTokens, usage.OutputTokens)
}
