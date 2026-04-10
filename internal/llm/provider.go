package llm

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

// Provider defines the interface for LLM providers
type Provider interface {
	Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error)
	GetModelInfo() ModelInfo
}

// Response represents an LLM response
type Response struct {
	Content      string
	TokensUsed   int
	Model        string
	FinishReason string
}

// ModelInfo holds information about the model
type ModelInfo struct {
	Name              string
	Provider          string
	MaxTokens         int
	SupportsToolCalls bool
}

// GenerateOption configures generation behavior
type GenerateOption func(*GenerateConfig)

// GenerateConfig holds generation configuration
type GenerateConfig struct {
	MaxTokens    int
	Temperature  float32
	SystemPrompt string
}

// WithMaxTokens sets max tokens
func WithMaxTokens(tokens int) GenerateOption {
	return func(c *GenerateConfig) {
		c.MaxTokens = tokens
	}
}

// WithTemperature sets temperature
func WithTemperature(temp float32) GenerateOption {
	return func(c *GenerateConfig) {
		c.Temperature = temp
	}
}

// WithSystemPrompt sets system prompt
func WithSystemPrompt(prompt string) GenerateOption {
	return func(c *GenerateConfig) {
		c.SystemPrompt = prompt
	}
}

// OpenAIProvider implements Provider for OpenAI
type OpenAIProvider struct {
	client *openai.Client
	model  string
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(apiKey, model string) Provider {
	if model == "" {
		model = openai.GPT4oMini
	}
	return &OpenAIProvider{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

// Generate generates a response from OpenAI
func (p *OpenAIProvider) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error) {
	config := &GenerateConfig{
		MaxTokens:   1000,
		Temperature: 0.7,
	}

	for _, opt := range opts {
		opt(config)
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: prompt,
		},
	}

	if config.SystemPrompt != "" {
		messages = append([]openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: config.SystemPrompt,
			},
		}, messages...)
	}

	resp, err := p.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model:       p.model,
			Messages:    messages,
			MaxTokens:   config.MaxTokens,
			Temperature: config.Temperature,
		},
	)

	if err != nil {
		return nil, fmt.Errorf("openai completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no completion choices returned")
	}

	return &Response{
		Content:      resp.Choices[0].Message.Content,
		TokensUsed:   resp.Usage.TotalTokens,
		Model:        resp.Model,
		FinishReason: string(resp.Choices[0].FinishReason),
	}, nil
}

// GetModelInfo returns model information
func (p *OpenAIProvider) GetModelInfo() ModelInfo {
	return ModelInfo{
		Name:              p.model,
		Provider:          "openai",
		MaxTokens:         128000,
		SupportsToolCalls: true,
	}
}

// Factory creates LLM providers based on configuration
type Factory struct {
	providerType string
	apiKey       string
	model        string
	baseURL      string // for custom providers
}

// NewFactory creates a new provider factory
func NewFactory(providerType, apiKey, model, baseURL string) *Factory {
	return &Factory{
		providerType: providerType,
		apiKey:       apiKey,
		model:        model,
		baseURL:      baseURL,
	}
}

// Create creates a provider instance
func (f *Factory) Create() (Provider, error) {
	switch f.providerType {
	case "openai":
		return NewOpenAIProvider(f.apiKey, f.model), nil
	case "custom", "byom":
		return NewCustomProvider(f.baseURL, f.apiKey, f.model), nil
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", f.providerType)
	}
}
