package llmclient

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/config"
)

func TestNormalizeAnthropicBaseURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"https://api.anthropic.com", "https://api.anthropic.com"},
		{"https://api.anthropic.com/", "https://api.anthropic.com"},
		{"https://api.router.example/v1", "https://api.router.example"},
		{"https://api.router.example/v1/", "https://api.router.example"},
	}
	for _, tt := range tests {
		if got := normalizeAnthropicBaseURL(tt.in); got != tt.want {
			t.Errorf("normalizeAnthropicBaseURL(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeGeminiBaseURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"https://gateway.example", "https://gateway.example"},
		{"https://gateway.example/", "https://gateway.example"},
		{"  https://gateway.example/gemini/  ", "https://gateway.example/gemini"},
	}
	for _, tt := range tests {
		if got := normalizeGeminiBaseURL(tt.in); got != tt.want {
			t.Errorf("normalizeGeminiBaseURL(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestZDREnforceable(t *testing.T) {
	tests := []struct {
		name, iface, baseURL string
		want                 bool
	}{
		{"openai wire at openrouter", config.LLMInterfaceOpenAI, "https://openrouter.ai/api/v1", true},
		{"openai wire elsewhere", config.LLMInterfaceOpenAI, "https://api.openai.com/v1", false},
		{"openai wire with no base url", config.LLMInterfaceOpenAI, "", false},
		// OpenRouter serves neither /v1/messages nor the genai endpoints, so
		// these are misconfigurations already; what matters is that they warn
		// rather than pass as protected.
		{"anthropic wire at openrouter", config.LLMInterfaceAnthropic, "https://openrouter.ai", false},
		{"gemini wire at openrouter", config.LLMInterfaceGemini, "https://openrouter.ai", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := zdrEnforceable(tt.iface, tt.baseURL); got != tt.want {
				t.Errorf("zdrEnforceable(%q, %q) = %v; want %v", tt.iface, tt.baseURL, got, tt.want)
			}
		})
	}
}
