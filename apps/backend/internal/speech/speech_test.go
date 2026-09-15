package speech

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var webmHead = []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81}

func TestNewIsTheNopWhenNotUsable(t *testing.T) {
	cases := map[string]Config{
		"switched off":     {Enabled: false, Provider: "groq", APIKey: "k"},
		"no key":           {Enabled: true, Provider: "groq"},
		"unknown provider": {Enabled: true, Provider: "whisperer", APIKey: "k"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			tr := New(cfg)
			if tr.Enabled() {
				t.Fatal("Enabled() = true for a transcriber that cannot reach a provider")
			}
			if tr.Model() != "" {
				t.Errorf("Model() = %q, want empty", tr.Model())
			}
			_, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/webm", "id")
			if !errors.Is(err, ErrDisabled) {
				t.Errorf("Transcribe error = %v, want ErrDisabled", err)
			}
		})
	}
}

func TestNewTakesTheProvidersDefaults(t *testing.T) {
	if got := New(Config{Enabled: true, Provider: "Groq", APIKey: "k"}).Model(); got != "whisper-large-v3-turbo" {
		t.Errorf("groq model = %q", got)
	}
	// The deployment default since 2026-09-15: a key and a name, and nothing else.
	if tr := New(Config{Enabled: true, Provider: "openrouter", APIKey: "k"}); !tr.Enabled() || tr.Model() != "openai/whisper-large-v3-turbo" {
		t.Errorf("openrouter: enabled %v, model %q", tr.Enabled(), tr.Model())
	}
	if got := New(Config{Enabled: true, Provider: "openai", APIKey: "k", Model: "gpt-4o-transcribe"}).Model(); got != "gpt-4o-transcribe" {
		t.Errorf("an explicit model was not kept: %q", got)
	}
	// A compatible endpoint that is neither provider needs a model; with one it
	// is usable without a provider name.
	if New(Config{Enabled: true, BaseURL: "http://127.0.0.1:1", APIKey: "k"}).Enabled() {
		t.Error("a base URL with no model was enabled")
	}
	if !New(Config{Enabled: true, BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}).Enabled() {
		t.Error("a base URL with a model was not enabled")
	}
}

type seen struct {
	path, auth, filename, partType string
	audio                          []byte
	fields                         map[string]string
}

func fakeProvider(t *testing.T, status int, body string, got *seen) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not a multipart request: %v", err)
		}
		got.fields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			got.fields[k] = v[0]
		}
		if fh := r.MultipartForm.File["file"]; len(fh) == 1 {
			got.filename = fh[0].Filename
			got.partType = fh[0].Header.Get("Content-Type")
			f, _ := fh[0].Open()
			got.audio, _ = io.ReadAll(f)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTranscribeSendsTheClipTheModelAndTheHint(t *testing.T) {
	var got seen
	srv := fakeProvider(t, 200, `{"text":"  berapa penjualan kemarin? ","language":"indonesian","duration":3.25}`, &got)
	tr := New(Config{Enabled: true, BaseURL: srv.URL, APIKey: "sk-test", Model: "whisper-large-v3-turbo"})

	audio := append(append([]byte{}, webmHead...), []byte("opus frames")...)
	out, err := tr.Transcribe(context.Background(), strings.NewReader(string(audio)), "audio/webm", "id")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if out.Text != "berapa penjualan kemarin?" || out.Seconds != 3.25 || out.Language != "indonesian" {
		t.Errorf("transcript = %+v", out)
	}
	if got.path != "/audio/transcriptions" || got.auth != "Bearer sk-test" {
		t.Errorf("request went to %q with auth %q", got.path, got.auth)
	}
	// The extension is how OpenAI's endpoint decides the format.
	if got.filename != "clip.webm" || got.partType != "audio/webm" || string(got.audio) != string(audio) {
		t.Errorf("file part = %q %q %d bytes", got.filename, got.partType, len(got.audio))
	}
	want := map[string]string{"model": "whisper-large-v3-turbo", "response_format": "verbose_json", "temperature": "0", "language": "id"}
	for k, v := range want {
		if got.fields[k] != v {
			t.Errorf("field %s = %q, want %q", k, got.fields[k], v)
		}
	}
}

// The deployment's model key is sent only to the host it was issued for — the
// lesson config.EffectiveEmbeddingAPIKey records — and never over a key of the
// transcriber's own.
func TestASharedKeyIsSentOnlyToItsOwnHost(t *testing.T) {
	var got seen
	srv := fakeProvider(t, 200, `{"text":"ok"}`, &got)
	transcribe := func(tr Transcriber) {
		t.Helper()
		if _, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/webm", ""); err != nil {
			t.Fatalf("Transcribe: %v", err)
		}
	}

	shared := New(Config{Enabled: true, BaseURL: srv.URL, Model: "m", SharedKey: "sk-or-model", SharedKeyBaseURL: srv.URL + "/api/v1"})
	if !shared.Enabled() {
		t.Fatal("a shared key for the same host did not enable the transcriber")
	}
	transcribe(shared)
	if got.auth != "Bearer sk-or-model" {
		t.Errorf("auth = %q, want the shared key", got.auth)
	}

	own := New(Config{Enabled: true, BaseURL: srv.URL, Model: "m", APIKey: "sk-own", SharedKey: "sk-or-model", SharedKeyBaseURL: srv.URL})
	transcribe(own)
	if got.auth != "Bearer sk-own" {
		t.Errorf("auth = %q, want the transcriber's own key over the shared one", got.auth)
	}

	// OpenRouter's default with the model key issued for openrouter.ai: usable.
	if !New(Config{Enabled: true, Provider: "openrouter", SharedKey: "k", SharedKeyBaseURL: "https://openrouter.ai/api/v1"}).Enabled() {
		t.Error("the model's OpenRouter key did not reach the OpenRouter transcriber")
	}
	// Another host, a URL with no scheme, and no URL at all: nothing is sent.
	for _, other := range []string{"https://api.openai.com/v1", "openrouter.ai/api/v1", ""} {
		if New(Config{Enabled: true, Provider: "openrouter", SharedKey: "k", SharedKeyBaseURL: other}).Enabled() {
			t.Errorf("a key issued for %q was offered to openrouter.ai", other)
		}
	}
	// Two URLs that name no host are not the same host: a custom endpoint written
	// without a scheme, beside a model with no base URL, gets no key.
	if New(Config{Enabled: true, BaseURL: "localhost:9000", Model: "m", SharedKey: "k", SharedKeyBaseURL: ""}).Enabled() {
		t.Error("two URLs with no host were taken for one host")
	}
}

// OpenRouter's answer: `usage.seconds` when there is no `duration`, and the
// charge. A `duration` the host did supply still wins, being the host's measure.
func TestTranscribeReadsWhatOpenRouterMeasuredAndCharged(t *testing.T) {
	cases := []struct {
		name, body  string
		wantSeconds float64
		wantCost    float64
	}{
		{"no duration", `{"text":"ok","usage":{"seconds":2.5,"cost":0.0000075}}`, 2.5, 0.0000075},
		{"a duration and usage", `{"text":"ok","duration":2.75,"usage":{"seconds":2.5,"cost":0.00001}}`, 2.75, 0.00001},
		{"usage with no cost", `{"text":"ok","usage":{"seconds":2.5}}`, 2.5, 0},
		{"no usage", `{"text":"ok","duration":3}`, 3, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got seen
			srv := fakeProvider(t, 200, c.body, &got)
			tr := New(Config{Enabled: true, Provider: "openrouter", BaseURL: srv.URL, APIKey: "k"})
			out, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/webm", "id")
			if err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if out.Seconds != c.wantSeconds || out.CostUSD != c.wantCost {
				t.Errorf("seconds %v, cost %v; want %v, %v", out.Seconds, out.CostUSD, c.wantSeconds, c.wantCost)
			}
			if got.fields["model"] != "openai/whisper-large-v3-turbo" {
				t.Errorf("model sent = %q, want the openrouter default", got.fields["model"])
			}
		})
	}
}

func TestNoHintSendsNoLanguage(t *testing.T) {
	var got seen
	srv := fakeProvider(t, 200, `{"text":"ok"}`, &got)
	tr := New(Config{Enabled: true, BaseURL: srv.URL, APIKey: "k", Model: "m"})
	out, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/ogg", "")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, sent := got.fields["language"]; sent {
		t.Error("an empty hint was sent as a language, which stops the provider detecting one")
	}
	// No duration in the answer is zero, for the caller to fall back from.
	if out.Seconds != 0 {
		t.Errorf("Seconds = %v, want 0", out.Seconds)
	}
}

func TestAProviderRefusalCarriesItsStatusAndOwnSentence(t *testing.T) {
	var got seen
	srv := fakeProvider(t, 400, `{"error":{"message":"audio file is too short","type":"invalid_request_error"}}`, &got)
	tr := New(Config{Enabled: true, BaseURL: srv.URL, APIKey: "k", Model: "m"})
	_, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/webm", "")
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != 400 || pe.Message != "audio file is too short" {
		t.Fatalf("error = %v, want a ProviderError 400 with the provider's message", err)
	}
}

func TestAnErrorPageIsNotQuoted(t *testing.T) {
	var got seen
	srv := fakeProvider(t, 502, `<html><body>Bad Gateway</body></html>`, &got)
	tr := New(Config{Enabled: true, BaseURL: srv.URL, APIKey: "k", Model: "m"})
	_, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/webm", "")
	if err == nil || strings.Contains(err.Error(), "html") {
		t.Fatalf("error = %v, want a summary rather than the page", err)
	}
}

func TestASlowProviderIsAnErrorNotAHang(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	tr := New(Config{Enabled: true, BaseURL: srv.URL, APIKey: "k", Model: "m", Timeout: 50 * time.Millisecond})
	start := time.Now()
	if _, err := tr.Transcribe(context.Background(), strings.NewReader("x"), "audio/webm", ""); err == nil {
		t.Fatal("a provider past its timeout returned no error")
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Errorf("took %v; the timeout did not bound the call", time.Since(start))
	}
}

func TestAcceptNeedsTheBytesToBeTheDeclaredAudio(t *testing.T) {
	mp4Head := []byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}
	wavHead := []byte("RIFF\x24\x00\x00\x00WAVEfmt ")
	cases := []struct {
		name, declared string
		head           []byte
		wantType       string
		wantExt        string
	}{
		{"chrome's webm, parameters dropped", "audio/webm;codecs=opus", webmHead, "audio/webm", "webm"},
		{"safari's mp4", "audio/mp4", mp4Head, "audio/mp4", "m4a"},
		{"ogg", "audio/ogg; codecs=opus", []byte("OggS\x00\x02"), "audio/ogg", "ogg"},
		{"mp3 with a tag", "audio/mpeg", []byte("ID3\x04\x00"), "audio/mpeg", "mp3"},
		{"mp3 without one", "audio/mpeg", []byte{0xFF, 0xFB, 0x90, 0x64}, "audio/mpeg", "mp3"},
		{"wav", "audio/wav", wavHead, "audio/wav", "wav"},
		{"flac", "audio/flac", []byte("fLaC\x00\x00"), "audio/flac", "flac"},
		{"a pdf called webm", "audio/webm", []byte("%PDF-1.7"), "", ""},
		{"webm bytes called mp3", "audio/mpeg", webmHead, "", ""},
		{"video, not audio", "video/webm", webmHead, "", ""},
		{"no type at all", "", webmHead, "", ""},
		{"too short to tell", "audio/wav", []byte("RIFF"), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mt, ext, ok := Accept(c.declared, c.head)
			if ok != (c.wantType != "") || mt != c.wantType || ext != c.wantExt {
				t.Errorf("Accept(%q) = %q %q %v, want %q %q", c.declared, mt, ext, ok, c.wantType, c.wantExt)
			}
		})
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for raw, want := range map[string]string{"id": "id", " EN ": "en", "id-ID": "id", "en_US": "en"} {
		if got, ok := NormalizeLanguage(raw); !ok || got != want {
			t.Errorf("NormalizeLanguage(%q) = %q %v, want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "ind", "indonesian", "i", "1d"} {
		if got, ok := NormalizeLanguage(raw); ok {
			t.Errorf("NormalizeLanguage(%q) = %q, want refused", raw, got)
		}
	}
}
