package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// maxResponseBytes bounds what is read back. A verbose transcript of the
// longest clip the route admits is a few kilobytes; a megabyte is a provider
// misbehaving, and reading it whole would be this process misbehaving with it.
const maxResponseBytes = 1 << 20

// ProviderError is a provider's refusal, carrying its status and its own
// sentence. The sentence is the provider's error message, never the request:
// nothing a person said is in it.
type ProviderError struct {
	Status  int
	Message string
}

func (e *ProviderError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("speech provider answered %d", e.Status)
	}
	return fmt.Sprintf("speech provider answered %d: %s", e.Status, e.Message)
}

// compatTranscriber speaks the `/audio/transcriptions` request OpenAI defined
// and Groq copied: one multipart POST, the file and the model, a JSON answer.
//
// One implementation for both, rather than a provider SDK each, for the reason
// email chose SMTP: the choice of provider is an operator's, and an interface
// with one real implementation per vendor would make it a code change. A
// provider that speaks something else goes behind Transcriber later without
// touching a caller.
type compatTranscriber struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func newClient(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }

func (t *compatTranscriber) Enabled() bool { return true }
func (t *compatTranscriber) Model() string { return t.model }

// Transcribe sends one clip and reads back what was heard.
//
// `verbose_json` rather than `json`, for one field: `duration`. It is how long
// the provider measured the clip to be, and it is what the usage row is billed
// on — a length the client declared is a number the client could have written.
// A provider or model that does not return it (OpenAI's gpt-4o transcription
// models answer `json` only) leaves Seconds at zero, and the caller falls back
// to the declared length and says so.
//
// Temperature zero, because a transcript is not a place for a model to be
// creative: the same clip should come back as the same words.
func (t *compatTranscriber) Transcribe(ctx context.Context, audio io.Reader, mediaType, langHint string) (Transcript, error) {
	ext, ok := extensionFor(mediaType)
	if !ok {
		return Transcript{}, fmt.Errorf("speech: %q is not an accepted audio type", mediaType)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="clip.`+ext+`"`)
	h.Set("Content-Type", mediaType)
	part, err := mw.CreatePart(h)
	if err != nil {
		return Transcript{}, fmt.Errorf("speech: build request: %w", err)
	}
	if _, err := io.Copy(part, audio); err != nil {
		return Transcript{}, fmt.Errorf("speech: read clip: %w", err)
	}
	fields := [][2]string{
		{"model", t.model},
		{"response_format", "verbose_json"},
		{"temperature", "0"},
	}
	if langHint != "" {
		fields = append(fields, [2]string{"language", langHint})
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return Transcript{}, fmt.Errorf("speech: build request: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return Transcript{}, fmt.Errorf("speech: build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return Transcript{}, fmt.Errorf("speech: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := t.client.Do(req)
	if err != nil {
		// *url.Error names the URL, which carries no credential — the key is a
		// header. Wrapped rather than stringified so a caller can still tell a
		// deadline from a refused connection.
		return Transcript{}, fmt.Errorf("speech: call provider: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Transcript{}, fmt.Errorf("speech: read provider answer: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Transcript{}, &ProviderError{Status: resp.StatusCode, Message: providerMessage(raw)}
	}

	var out struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Transcript{}, errors.New("speech: provider answer is not the expected JSON")
	}
	return Transcript{
		Text:     strings.TrimSpace(out.Text),
		Language: out.Language,
		Seconds:  out.Duration,
	}, nil
}

// providerMessage pulls the provider's own sentence out of an error body —
// both providers answer `{"error": {"message": …}}` — and bounds it. A body
// that is not that shape is summarised by its length rather than quoted: an
// HTML error page from a proxy in front of the provider is not a sentence
// anybody should be shown.
func providerMessage(raw []byte) string {
	var shaped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &shaped) == nil && shaped.Error.Message != "" {
		msg := shaped.Error.Message
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return msg
	}
	if len(raw) == 0 {
		return ""
	}
	return fmt.Sprintf("unrecognised %d-byte answer", len(raw))
}
