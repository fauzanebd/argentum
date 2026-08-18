// Package llmclient constructs an agent-sdk-go LLM for a given wire protocol
// (openai, anthropic, gemini), model and base URL. It is the only place that
// knows which SDK client a provider string maps to.
package llmclient

import (
	"context"
	"net/http"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/anthropic"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/gemini"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/openai"
	oaisdk "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/llmusage"
	"github.com/fauzanebd/argentum/internal/llmzdr"
)

// Spec is the minimum input Build needs to construct an LLM client. Used by
// both the env-default builders below and the per-tenant resolver in
// internal/llmtenant.
type Spec struct {
	Interface string // "openai" | "anthropic" | "gemini"
	APIKey    string
	Model     string
	BaseURL   string
	ZDR       bool // LLM_ZDR: pin routing to OpenRouter zero-data-retention endpoints
}

// Build constructs an LLM client from a fully-resolved spec.
func Build(spec Spec) (interfaces.LLM, error) {
	return build(context.Background(), spec)
}

// BuildPrimary constructs the main agent LLM per LLM_INTERFACE / LLM_PROVIDER.
func BuildPrimary(cfg *config.Config) (interfaces.LLM, error) {
	return Build(Spec{
		Interface: cfg.EffectiveLLMInterface(),
		APIKey:    cfg.LLMAPIKey,
		Model:     cfg.LLMModel,
		BaseURL:   cfg.LLMBaseURL,
		ZDR:       cfg.LLMZDR,
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
		ZDR:       cfg.LLMZDR,
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
	return Build(Spec{Interface: iface, APIKey: apiKey, Model: model, BaseURL: baseURL, ZDR: cfg.LLMZDR})
}

func build(ctx context.Context, spec Spec) (interfaces.LLM, error) {
	iface, apiKey, model, baseURL := spec.Interface, spec.APIKey, spec.Model, spec.BaseURL
	if spec.ZDR && !zdrEnforceable(iface, baseURL) {
		// A privacy switch that silently does nothing is worse than no switch:
		// the operator believes the prompts are protected. Logged per client
		// build, which is rare — llmtenant caches them.
		logrus.WithFields(logrus.Fields{
			"interface": iface,
			"base_url":  baseURL,
		}).Warn("LLM_ZDR is set but this endpoint cannot enforce it; requests will route with no zero-data-retention guarantee")
	}
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
		client := openai.NewClient(apiKey, opts...)
		installUsageTap(client, apiKey, baseURL, spec.ZDR)
		return client, nil
	}
}

// zdrEnforceable reports whether a spec can actually carry provider.zdr onto
// the wire. OpenRouter speaks only the OpenAI wire format, so the Anthropic
// and Gemini clients — which POST to /v1/messages and the genai endpoints —
// have nowhere to put the preference even when pointed at an OpenRouter host.
func zdrEnforceable(iface, baseURL string) bool {
	switch iface {
	case config.LLMInterfaceAnthropic, config.LLMInterfaceGemini:
		return false
	}
	return llmzdr.TargetsOpenRouter(baseURL)
}

// defaultOpenAIBaseURL mirrors agent-sdk-go's own default; installUsageTap has
// to rebuild the services, and rebuilding them without a base URL would
// silently repoint a gateway-configured client at api.openai.com.
const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// installUsageTap replaces the SDK's HTTP client with one that reads token
// usage off the SSE wire (internal/llmusage).
//
// Finding C-2: agent-sdk-go's OpenAI client asks for
// stream_options.include_usage and then drops the usage chunk in
// GenerateWithToolsStream — the path every agent turn takes — so streaming
// turns recorded zero tokens for the primary model. The provider does send the
// numbers; only the SDK's plumbing loses them. Reading the response body is
// cheaper and more accurate than forking the SDK or estimating with a local
// tokenizer, and it covers every iteration of the tool-calling loop.
//
// The tap only acts when app.MeteredLLM has put a collector in the request
// context and the response is text/event-stream, so non-streaming calls keep
// being metered from the SDK's own LLMResponse.Usage with no double counting.
func installUsageTap(c *openai.OpenAIClient, apiKey, baseURL string, zdr bool) {
	if c == nil {
		return
	}
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = defaultOpenAIBaseURL
	}
	// The ZDR rewriter sits underneath the usage tap: it edits the request on
	// the way out, the tap reads the response on the way back, and neither
	// needs to know about the other.
	var base http.RoundTripper
	if zdr {
		base = llmzdr.New(nil)
	}
	httpClient := llmusage.NewClient(base)
	reqOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(url),
		option.WithHTTPClient(httpClient),
	}
	c.Client = oaisdk.NewClient(reqOpts...)
	c.ChatService = oaisdk.NewChatService(reqOpts...)
	c.ResponseService = oaisdk.NewClient(reqOpts...)
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
