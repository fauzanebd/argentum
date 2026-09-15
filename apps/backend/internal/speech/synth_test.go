package speech

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewSynthesizerIsTheNopWhenNotUsable(t *testing.T) {
	cases := map[string]SynthConfig{
		"switched off": {Enabled: false, Provider: "openai", APIKey: "k"},
		"no key":       {Enabled: true, Provider: "openai"},
		// Groq has no row: its speech models do not speak Indonesian.
		"groq":                              {Enabled: true, Provider: "groq", APIKey: "k"},
		"a base URL with no model or voice": {Enabled: true, BaseURL: "http://127.0.0.1:1", APIKey: "k"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewSynthesizer(cfg)
			if s.Enabled() || s.Model() != "" || s.Voice() != "" {
				t.Fatalf("enabled %v, model %q, voice %q for a synthesiser that cannot reach a provider", s.Enabled(), s.Model(), s.Voice())
			}
			if _, err := s.Speak(context.Background(), "halo", ""); !errors.Is(err, ErrDisabled) {
				t.Errorf("Speak error = %v, want ErrDisabled", err)
			}
		})
	}
}

func TestNewSynthesizerTakesTheProvidersDefaults(t *testing.T) {
	s := NewSynthesizer(SynthConfig{Enabled: true, Provider: "OpenAI", APIKey: "k"})
	if s.Model() != "tts-1" || s.Voice() != "alloy" {
		t.Errorf("openai defaults = %q, %q", s.Model(), s.Voice())
	}
	// The deployment default since 2026-09-15: OpenRouter's one voice whose maker
	// documents Indonesian.
	s = NewSynthesizer(SynthConfig{Enabled: true, Provider: "openrouter", APIKey: "k"})
	if !s.Enabled() || s.Model() != "google/gemini-3.1-flash-tts-preview" || s.Voice() != "Kore" {
		t.Errorf("openrouter defaults: enabled %v, %q, %q", s.Enabled(), s.Model(), s.Voice())
	}
	s = NewSynthesizer(SynthConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m", Voice: "v"})
	if !s.Enabled() || s.Model() != "m" || s.Voice() != "v" {
		t.Errorf("a compatible endpoint with a model and a voice: enabled %v, %q, %q", s.Enabled(), s.Model(), s.Voice())
	}
}

// The synthesiser takes the model's key on Config's terms: same host only, and
// never over a key of its own.
func TestNewSynthesizerSendsASharedKeyOnlyToItsOwnHost(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("ID3\x04\x00frames"))
	}))
	defer srv.Close()

	s := NewSynthesizer(SynthConfig{Enabled: true, Provider: "openrouter", BaseURL: srv.URL, SharedKey: "sk-or-model", SharedKeyBaseURL: srv.URL + "/api/v1"})
	if _, err := s.Speak(context.Background(), "halo", ""); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if got := auth.Load(); got != "Bearer sk-or-model" {
		t.Errorf("auth = %v, want the shared key", got)
	}
	if NewSynthesizer(SynthConfig{Enabled: true, Provider: "openrouter", SharedKey: "k", SharedKeyBaseURL: "https://api.openai.com/v1"}).Enabled() {
		t.Error("a key issued for api.openai.com was offered to openrouter.ai")
	}
}

func TestSpeakSendsTheRequestAndReturnsMP3(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/speech" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request %s with auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3\x04\x00frames"))
	}))
	defer srv.Close()

	s := NewSynthesizer(SynthConfig{Enabled: true, Provider: "openai", BaseURL: srv.URL, APIKey: "k"})
	audio, err := s.Speak(context.Background(), "  Penjualan sekitar 1,2 juta.  ", "")
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if audio.MediaType != "audio/mpeg" || audio.Ext != "mp3" || !strings.HasPrefix(string(audio.Data), "ID3") {
		t.Errorf("audio = %+v", audio)
	}
	want := map[string]string{"model": "tts-1", "voice": "alloy", "response_format": "mp3", "input": "Penjualan sekitar 1,2 juta."}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("request %s = %q, want %q", k, got[k], v)
		}
	}
}

// A 200 that is not audio would be cached under the message's id and played to
// everyone who pressed the button; a refusal carries the provider's status.
func TestSpeakRefusesAnAnswerThatIsNotAudio(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"a proxy's page served as 200": {200, "<html>gateway</html>"},
		"a rate limit":                 {429, `{"error":{"message":"rate limited"}}`},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		s := NewSynthesizer(SynthConfig{Enabled: true, Provider: "openai", BaseURL: srv.URL, APIKey: "k"})
		_, err := s.Speak(context.Background(), "halo", "")
		srv.Close()
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		var pe *ProviderError
		if c.status != 200 && (!errors.As(err, &pe) || pe.Status != c.status || pe.Message != "rate limited") {
			t.Errorf("%s: error = %v, want the provider's status and sentence", name, err)
		}
	}
}

func TestSpeakRefusesTextOverTheProviderLimitBeforeSendingIt(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	s := NewSynthesizer(SynthConfig{Enabled: true, Provider: "openai", BaseURL: srv.URL, APIKey: "k"})
	if _, err := s.Speak(context.Background(), strings.Repeat("a", MaxSpeakChars+1), ""); err == nil {
		t.Error("no error for text over the limit")
	}
	if calls.Load() != 0 {
		t.Error("the provider was called")
	}
}
