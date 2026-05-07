package llmclient

import (
	"context"
	"net/http"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/anthropic"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/gemini"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/openai"
	"google.golang.org/genai"

	"github.com/fauzanebd/argentum/internal/config"
)

// BuildPrimary constructs the main agent LLM per LLM_INTERFACE / LLM_PROVIDER.
func BuildPrimary(cfg *config.Config) (interfaces.LLM, error) {
	return build(context.Background(), cfg.EffectiveLLMInterface(), cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMBaseURL)
}

// BuildLight constructs the light LLM for guardrails, classification, and summaries.
// When LIGHT_LLM_API_KEY is empty, it uses the same client stack as BuildPrimary.
func BuildLight(cfg *config.Config) (interfaces.LLM, error) {
	if cfg.LightLLMAPIKey == "" {
		return BuildPrimary(cfg)
	}
	return build(context.Background(), cfg.EffectiveLightLLMInterface(), cfg.LightLLMAPIKey, cfg.LightLLMModel, cfg.LightLLMBaseURL)
}

func build(ctx context.Context, iface, apiKey, model, baseURL string) (interfaces.LLM, error) {
	switch iface {
	case config.LLMInterfaceAnthropic:
		opts := []anthropic.Option{}
		if model != "" {
			opts = append(opts, anthropic.WithModel(model))
		}
		if u := normalizeAnthropicBaseURL(baseURL); u != "" {
			opts = append(opts, anthropic.WithBaseURL(u))
		}
		return anthropic.NewClient(apiKey, opts...), nil
	case config.LLMInterfaceGemini:
		opts := []gemini.Option{gemini.WithAPIKey(apiKey)}
		if model != "" {
			opts = append(opts, gemini.WithModel(model))
		}
		if u := normalizeGeminiBaseURL(baseURL); u != "" {
			key := strings.TrimSpace(apiKey)
			// google.golang.org/genai sends x-goog-api-key by default. Many third-party / OpenAI-style
			// gateways only accept Authorization: Bearer, which yields 401 "API key is required".
			hdr := http.Header{}
			hdr.Set("Authorization", "Bearer "+key)
			gc, err := genai.NewClient(ctx, &genai.ClientConfig{
				Backend: genai.BackendGeminiAPI,
				APIKey:  key,
				HTTPOptions: genai.HTTPOptions{
					BaseURL: u,
					Headers: hdr,
				},
			})
			if err != nil {
				return nil, err
			}
			opts = append([]gemini.Option{gemini.WithClient(gc)}, opts...)
		}
		return gemini.NewClient(ctx, opts...)
	default:
		opts := []openai.Option{}
		if model != "" {
			opts = append(opts, openai.WithModel(model))
		}
		if baseURL != "" {
			opts = append(opts, openai.WithBaseURL(baseURL))
		}
		return openai.NewClient(apiKey, opts...), nil
	}
}

// normalizeAnthropicBaseURL fixes LLM_BASE_URL for agent-sdk-go's Anthropic client, which always
// POSTs to {BaseURL}/v1/messages. Many gateways document an OpenAI-style host ending in /v1; keeping
// that suffix yields .../v1/v1/messages and a 404 "Unsupported endpoint".
func normalizeAnthropicBaseURL(base string) string {
	s := strings.TrimRight(strings.TrimSpace(base), "/")
	for strings.HasSuffix(s, "/v1") {
		s = strings.TrimSuffix(s, "/v1")
		s = strings.TrimRight(s, "/")
	}
	return s
}

// normalizeGeminiBaseURL trims whitespace and a trailing slash for google.golang.org/genai HTTPOptions.BaseURL.
func normalizeGeminiBaseURL(base string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/")
}
