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

// Spec is the minimum input Build needs to construct an LLM client. Used by
// both the env-default builders below and the per-tenant resolver in
// internal/llmtenant.
type Spec struct {
	Interface string // "openai" | "anthropic" | "gemini"
	APIKey    string
	Model     string
	BaseURL   string
}

// Build constructs an LLM client from a fully-resolved spec.
func Build(spec Spec) (interfaces.LLM, error) {
	return build(context.Background(), spec.Interface, spec.APIKey, spec.Model, spec.BaseURL)
}

// BuildPrimary constructs the main agent LLM per LLM_INTERFACE / LLM_PROVIDER.
func BuildPrimary(cfg *config.Config) (interfaces.LLM, error) {
	return Build(Spec{
		Interface: cfg.EffectiveLLMInterface(),
		APIKey:    cfg.LLMAPIKey,
		Model:     cfg.LLMModel,
		BaseURL:   cfg.LLMBaseURL,
	})
}

// BuildLight constructs the light LLM for guardrails, classification, and summaries.
// When LIGHT_LLM_API_KEY is empty, it uses the same client stack as BuildPrimary.
func BuildLight(cfg *config.Config) (interfaces.LLM, error) {
	if cfg.LightLLMAPIKey == "" {
		return BuildPrimary(cfg)
	}
	return Build(Spec{
		Interface: cfg.EffectiveLightLLMInterface(),
		APIKey:    cfg.LightLLMAPIKey,
		Model:     cfg.LightLLMModel,
		BaseURL:   cfg.LightLLMBaseURL,
	})
}

// BuildClassifier constructs the topic-classifier LLM. It reuses the light
// LLM's credentials, interface, and base URL but lets LLM_CLASSIFIER_MODEL
// pin a cheaper model (e.g. gpt-5-nano) for the RELATED/NEW classification.
// Returns BuildLight unchanged when LLM_CLASSIFIER_MODEL is empty.
func BuildClassifier(cfg *config.Config) (interfaces.LLM, error) {
	model := strings.TrimSpace(cfg.ClassifierModel)
	if model == "" {
		return BuildLight(cfg)
	}
	apiKey := cfg.LightLLMAPIKey
	iface := cfg.EffectiveLightLLMInterface()
	baseURL := cfg.LightLLMBaseURL
	if apiKey == "" {
		apiKey = cfg.LLMAPIKey
		iface = cfg.EffectiveLLMInterface()
		baseURL = cfg.LLMBaseURL
	}
	return Build(Spec{Interface: iface, APIKey: apiKey, Model: model, BaseURL: baseURL})
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
