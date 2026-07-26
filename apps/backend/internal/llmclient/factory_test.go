package llmclient

import "testing"

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
