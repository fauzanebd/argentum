package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirupsen/logrus"
)

// Audio is speech a synthesiser produced.
type Audio struct {
	Data      []byte
	MediaType string
	Ext       string
}

// Synthesizer reads text aloud (T-W8).
//
// It is handed text that is already speakable — Speakable's output, checked
// against the written answer — and does nothing to it. Deciding what may be said
// is not a synthesiser's job, and a provider that "tidied" its input would be a
// second, unchecked reduction behind the checked one.
type Synthesizer interface {
	// Speak synthesises text in voice, or in the configured voice when voice is
	// empty.
	Speak(ctx context.Context, text, voice string) (Audio, error)
	// Enabled reports whether text sent here would reach a provider.
	Enabled() bool
	// Model names what synthesised, for the usage row. Empty when disabled.
	Model() string
	// Voice is the configured voice. Empty when disabled.
	Voice() string
}

// SynthConfig is what a deployment sets.
//
// **Its own provider, not Config's.** T-W7's default is Groq, and Groq cannot
// read Indonesian aloud: its speech models (Orpheus) speak English and Saudi
// Arabic, take 200 characters a request and answer WAV only. OpenAI's speak the
// languages Whisper hears, Indonesian among them. So the deployment most likely
// to exist — Groq transcribing Indonesian — would have no voice out at all if the
// two shared a setting.
type SynthConfig struct {
	Enabled  bool
	Provider string
	APIKey   string
	// BaseURL overrides the provider's, for a compatible endpoint or a test
	// server. A base URL with no known provider needs Model and Voice as well.
	BaseURL string
	Model   string
	Voice   string
	Timeout time.Duration
}

// MaxSpeakChars is the longest text one request carries: OpenAI's limit on
// `/audio/speech` input. Speakable holds a spoken answer well under it, so
// meeting this is a caller not using Speakable.
const MaxSpeakChars = 4096

// maxAudioBytes bounds what is read back. The longest text Speakable admits is
// under two minutes of speech, a megabyte or two of MP3.
const maxAudioBytes = 16 << 20

// nopSynthesizer is what a deployment with no voice out gets, for
// nopTranscriber's reasons.
type nopSynthesizer struct{}

func (nopSynthesizer) Speak(context.Context, string, string) (Audio, error) {
	return Audio{}, ErrDisabled
}
func (nopSynthesizer) Enabled() bool { return false }
func (nopSynthesizer) Model() string { return "" }
func (nopSynthesizer) Voice() string { return "" }

type voiceProvider struct {
	baseURL, model, voice string
}

// voiceProviders are the endpoints with defaults.
//
// `tts-1` rather than `gpt-4o-mini-tts`, although the second is newer, for one
// reason: billing. `tts-1` is priced per character of input, which this process
// knows exactly. `gpt-4o-mini-tts` is priced per audio *token* produced, and
// `/audio/speech` answers with the audio and no usage — so its bill could only
// be an estimate. A deployment that wants it sets SPEECH_TTS_MODEL, and
// usage_speech.go says what that estimate is. `alloy` because every OpenAI
// speech model has it.
var voiceProviders = map[string]voiceProvider{
	"openai": {baseURL: "https://api.openai.com/v1", model: "tts-1", voice: "alloy"},
}

// NewSynthesizer builds a Synthesizer from config. Like New, it never returns an
// error: a deployment without voice out is a valid deployment, and it logs once
// saying which part is missing.
func NewSynthesizer(c SynthConfig) Synthesizer {
	name := strings.ToLower(strings.TrimSpace(c.Provider))
	p, known := voiceProviders[name]
	if b := strings.TrimSpace(c.BaseURL); b != "" {
		p.baseURL = strings.TrimRight(b, "/")
		known = true
	}
	if m := strings.TrimSpace(c.Model); m != "" {
		p.model = m
	}
	if v := strings.TrimSpace(c.Voice); v != "" {
		p.voice = v
	}
	hasKey := strings.TrimSpace(c.APIKey) != ""
	if !c.Enabled || !known || !hasKey || p.model == "" || p.voice == "" {
		fields := logrus.Fields{
			"enabled":   c.Enabled,
			"provider":  name,
			"known":     known,
			"has_key":   hasKey,
			"has_model": p.model != "",
			"has_voice": p.voice != "",
		}
		if c.Enabled {
			logrus.WithFields(fields).Warn("speech output is enabled but not usable; the answer audio route is not registered")
		} else {
			logrus.WithFields(fields).Info("speech output is not configured; the answer audio route is not registered")
		}
		return nopSynthesizer{}
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	logrus.WithFields(logrus.Fields{
		"provider": name, "base_url": p.baseURL, "model": p.model, "voice": p.voice,
	}).Info("speech output enabled")
	return &compatSynthesizer{
		baseURL: p.baseURL,
		model:   p.model,
		voice:   p.voice,
		apiKey:  strings.TrimSpace(c.APIKey),
		client:  newClient(c.Timeout),
	}
}

// compatSynthesizer speaks OpenAI's `/audio/speech` request: JSON in, audio
// bytes out. compatTranscriber's argument for one implementation holds here too.
type compatSynthesizer struct {
	baseURL, model, voice, apiKey string
	client                        *http.Client
}

func (s *compatSynthesizer) Enabled() bool { return true }
func (s *compatSynthesizer) Model() string { return s.model }
func (s *compatSynthesizer) Voice() string { return s.voice }

// Speak asks for MP3, because it is the one format every browser's <audio> plays
// and the smallest of the ones the provider offers that is.
//
// The answer is checked to be MP3 before it is returned. A proxy's error page
// served with a 200 would otherwise be cached under the message's id and played
// to everyone who pressed the button until it expired.
func (s *compatSynthesizer) Speak(ctx context.Context, text, voice string) (Audio, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Audio{}, errors.New("speech: nothing to say")
	}
	if n := utf8.RuneCountInString(text); n > MaxSpeakChars {
		return Audio{}, fmt.Errorf("speech: %d characters is over the %d one request carries", n, MaxSpeakChars)
	}
	if strings.TrimSpace(voice) == "" {
		voice = s.voice
	}
	body, err := json.Marshal(map[string]string{
		"model":           s.model,
		"input":           text,
		"voice":           voice,
		"response_format": "mp3",
	})
	if err != nil {
		return Audio{}, fmt.Errorf("speech: build request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return Audio{}, fmt.Errorf("speech: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return Audio{}, fmt.Errorf("speech: call provider: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes+1))
	if err != nil {
		return Audio{}, fmt.Errorf("speech: read provider answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Audio{}, &ProviderError{Status: resp.StatusCode, Message: providerMessage(raw)}
	}
	if len(raw) > maxAudioBytes {
		return Audio{}, fmt.Errorf("speech: provider answer is over %d bytes", maxAudioBytes)
	}
	if !mp3.magic(raw) {
		return Audio{}, errors.New("speech: provider answer is not MP3 audio")
	}
	return Audio{Data: raw, MediaType: "audio/mpeg", Ext: mp3.ext}, nil
}
