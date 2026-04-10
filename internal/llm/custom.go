package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// CustomProvider implements LLM provider for BYOM (Bring Your Own Model)
type CustomProvider struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewCustomProvider creates a new custom LLM provider
func NewCustomProvider(baseURL, apiKey, model string) Provider {
	return &CustomProvider{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Generate generates a response from custom LLM endpoint
func (p *CustomProvider) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error) {
	config := &GenerateConfig{
		MaxTokens:   1000,
		Temperature: 0.7,
	}

	for _, opt := range opts {
		opt(config)
	}

	// Build OpenAI-compatible request
	messages := []map[string]string{
		{"role": "user", "content": prompt},
	}

	if config.SystemPrompt != "" {
		messages = append([]map[string]string{
			{"role": "system", "content": config.SystemPrompt},
		}, messages...)
	}

	payload := map[string]interface{}{
		"model":       p.model,
		"messages":    messages,
		"max_tokens":  config.MaxTokens,
		"temperature": config.Temperature,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom LLM request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("custom LLM returned status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no completion choices returned")
	}

	return &Response{
		Content:      result.Choices[0].Message.Content,
		TokensUsed:   result.Usage.TotalTokens,
		Model:        result.Model,
		FinishReason: result.Choices[0].FinishReason,
	}, nil
}

// GetModelInfo returns model information
func (p *CustomProvider) GetModelInfo() ModelInfo {
	return ModelInfo{
		Name:              p.model,
		Provider:          "custom",
		MaxTokens:         128000,
		SupportsToolCalls: false,
	}
}

// FallbackManager manages LLM providers with fallback strategy
type FallbackManager struct {
	providers []Provider
	current   int
}

// NewFallbackManager creates a fallback manager
func NewFallbackManager(providers ...Provider) *FallbackManager {
	return &FallbackManager{
		providers: providers,
		current:   0,
	}
}

// Generate tries providers in order until one succeeds
func (fm *FallbackManager) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error) {
	var lastErr error

	for i, provider := range fm.providers {
		response, err := provider.Generate(ctx, prompt, opts...)
		if err == nil {
			if i > 0 {
				logrus.Infof("Fallback to provider %d succeeded", i)
			}
			return response, nil
		}

		lastErr = err
		logrus.Warnf("Provider %d failed: %v, trying fallback...", i, err)
	}

	return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// GetModelInfo returns info for current provider
func (fm *FallbackManager) GetModelInfo() ModelInfo {
	if fm.current < len(fm.providers) {
		return fm.providers[fm.current].GetModelInfo()
	}
	return ModelInfo{Name: "fallback", Provider: "unknown"}
}

// EmbeddingProvider provides text embeddings
type EmbeddingProvider struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewEmbeddingProvider creates an embedding provider
func NewEmbeddingProvider(baseURL, apiKey, model string) *EmbeddingProvider {
	return &EmbeddingProvider{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GenerateEmbedding creates embeddings for text
func (ep *EmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	payload := map[string]interface{}{
		"model": ep.model,
		"input": text,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ep.baseURL+"/v1/embeddings", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if ep.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.apiKey)
	}

	resp, err := ep.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding request failed with status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return result.Data[0].Embedding, nil
}

// SimpleEmbeddingProvider provides simple embeddings for development
type SimpleEmbeddingProvider struct{}

// NewSimpleEmbeddingProvider creates a simple embedding provider
func NewSimpleEmbeddingProvider() *SimpleEmbeddingProvider {
	return &SimpleEmbeddingProvider{}
}

// GenerateEmbedding creates simple bag-of-words embeddings
func (sep *SimpleEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	// Simple embedding: character frequency vector (128 dimensions)
	embedding := make([]float64, 128)
	for _, char := range text {
		idx := int(char) % 128
		embedding[idx]++
	}

	// Normalize
	norm := 0.0
	for _, v := range embedding {
		norm += v * v
	}
	norm = 1.0 / (norm + 0.0001)
	for i := range embedding {
		embedding[i] *= norm
	}

	return embedding, nil
}

// ProviderFactory creates LLM providers with fallback
type ProviderFactory struct {
	configs []ProviderConfig
}

// ProviderConfig holds provider configuration
type ProviderConfig struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	Priority int    `json:"priority"`
}

// NewProviderFactory creates a provider factory
func NewProviderFactory(configs []ProviderConfig) *ProviderFactory {
	return &ProviderFactory{configs: configs}
}

// CreateProviders creates providers with fallback
func (pf *ProviderFactory) CreateProviders() (Provider, *EmbeddingProvider, error) {
	var providers []Provider
	var embedProvider *EmbeddingProvider

	for _, cfg := range pf.configs {
		switch cfg.Provider {
		case "openai":
			providers = append(providers, NewOpenAIProvider(cfg.APIKey, cfg.Model))
			embedProvider = NewEmbeddingProvider("https://api.openai.com", cfg.APIKey, "text-embedding-3-small")
		case "custom", "byom":
			providers = append(providers, NewCustomProvider(cfg.BaseURL, cfg.APIKey, cfg.Model))
		}
	}

	if len(providers) == 0 {
		return nil, nil, fmt.Errorf("no valid providers configured")
	}

	if len(providers) == 1 {
		return providers[0], embedProvider, nil
	}

	return NewFallbackManager(providers...), embedProvider, nil
}
