// Package speech turns a question somebody said out loud into text (T-W7).
//
// It transcribes and nothing else. **A transcript is not a turn**: it goes back
// to the person who spoke, who sends it, edits it or throws it away (roadmap 11,
// decision 13). "tiga ratus juta" and "tiga puluh juta" are one syllable and a
// factor of ten apart, and a misheard multiplier would become a question the
// agent answers perfectly and uselessly — grounded, cited and wrong. So nothing
// in this package, and nothing that calls it, creates a message.
//
// Speaking an answer is T-W8's, behind its own interface and its own nop, so a
// deployment can have one without the other.
package speech

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Transcript is what a provider heard.
type Transcript struct {
	Text string
	// Language is the language the provider reports hearing, when it reports
	// one. Informational: the hint sent is what decided the transcription.
	Language string
	// Seconds is the clip's length as the provider measured it, or zero when
	// the provider did not say. It is the figure a clip is billed on, because it
	// is the only one nobody on the client side could have written.
	Seconds float64
	// CostUSD is what the provider says this transcription cost, when it says:
	// OpenRouter answers `usage.cost`, Groq and OpenAI answer nothing. When it is
	// present it is what is billed, because it is the charge and not a price
	// table's guess at one — and which host OpenRouter routed a clip to is not
	// something this process can know.
	CostUSD float64
}

// Transcriber is the one thing a caller needs.
type Transcriber interface {
	// Transcribe sends one clip. mediaType is the clip's type as Accept
	// normalised it, and langHint an ISO-639-1 code — or empty, which lets the
	// provider detect the language rather than assume one.
	Transcribe(ctx context.Context, audio io.Reader, mediaType, langHint string) (Transcript, error)
	// Enabled reports whether a clip sent here would reach a provider. The
	// route is not registered when it is false (decision 15: a dead provider is
	// a disabled button and a typed question, never a failure).
	Enabled() bool
	// Model names what transcribed, for the usage row. Empty when disabled.
	Model() string
}

// Config is what a deployment sets.
type Config struct {
	Enabled bool
	// Provider names the endpoint's defaults: "openrouter", "groq" or "openai".
	// All three speak the same `/audio/transcriptions` request, which is the
	// point — choosing between them is a configuration change, not a new
	// implementation. Indonesian accuracy (research 08 §6, unknowns 1 and 2) is
	// still unmeasured on any of them.
	Provider string
	APIKey   string
	// SharedKey is a key this deployment holds for another endpoint — its
	// model's — offered for when one account serves both. It is sent only when
	// APIKey is empty and the transcriber's base URL is on the same host as
	// SharedKeyBaseURL. The host check is the point of it:
	// config.EffectiveEmbeddingAPIKey records the day a fallback without one
	// sent an OpenRouter key to api.openai.com.
	SharedKey        string
	SharedKeyBaseURL string
	// BaseURL overrides the provider's, for a compatible endpoint that is
	// neither — or a test server.
	BaseURL string
	// Model overrides the provider's default model.
	Model   string
	Timeout time.Duration
}

// ErrDisabled is returned by the no-op transcriber. It says nothing went wrong:
// there is nowhere to send a clip on this deployment.
var ErrDisabled = errors.New("speech is not configured on this deployment")

// nopTranscriber is what a deployment with no speech provider gets. It is not
// an error type, for email.nopSender's reason: a deployment without voice is a
// valid deployment, and it is every existing one. What it must not do is look
// like it works, which is the mistake T-P8's embedder resolver made when it
// returned no client and no error and the boot log printed "enabled".
type nopTranscriber struct{}

func (nopTranscriber) Transcribe(context.Context, io.Reader, string, string) (Transcript, error) {
	return Transcript{}, ErrDisabled
}
func (nopTranscriber) Enabled() bool { return false }
func (nopTranscriber) Model() string { return "" }

// provider is one row of what this package knows how to reach.
type provider struct {
	baseURL string
	model   string
}

// providers are the endpoints with a default.
//
// OpenRouter is the deployment default since 2026-09-15, the owner's call
// (research 08 §2e): the product already holds an OpenRouter key for its model,
// and one key is one account, one bill and one place to revoke it. What that
// costs is written down there — OpenRouter cannot be told which host hears a
// clip, and one of its speech endpoints is on its zero-retention list. Its
// default model is the same Whisper that Groq serves directly, so the choice
// changes who is paid, not what is heard.
var providers = map[string]provider{
	"openrouter": {baseURL: "https://openrouter.ai/api/v1", model: "openai/whisper-large-v3-turbo"},
	"groq":       {baseURL: "https://api.groq.com/openai/v1", model: "whisper-large-v3-turbo"},
	"openai":     {baseURL: "https://api.openai.com/v1", model: "whisper-1"},
}

// New builds a Transcriber from config. It never returns an error: a deployment
// whose speech provider is missing or misnamed gets the no-op transcriber and
// one log line saying which, because failing to boot over an optional input
// would take the whole product down for a button most people never press.
func New(c Config) Transcriber {
	name := strings.ToLower(strings.TrimSpace(c.Provider))
	p, known := providers[name]
	if strings.TrimSpace(c.BaseURL) != "" {
		p.baseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
		known = true
	}
	if strings.TrimSpace(c.Model) != "" {
		p.model = strings.TrimSpace(c.Model)
	}
	key, keySource := resolveKey(c.APIKey, c.SharedKey, c.SharedKeyBaseURL, p.baseURL)
	hasKey := key != ""
	if !c.Enabled || !known || !hasKey || p.model == "" {
		fields := logrus.Fields{
			"enabled":   c.Enabled,
			"provider":  name,
			"known":     known,
			"has_key":   hasKey,
			"has_model": p.model != "",
		}
		// Said once, at startup. Info when voice is simply off; Warn when an
		// operator switched it on and it still is not, because that deployment
		// believes it has voice.
		if c.Enabled {
			logrus.WithFields(fields).Warn("speech is enabled but not usable; the voice route is not registered")
		} else {
			logrus.WithFields(fields).Info("speech is not configured; the voice route is not registered")
		}
		return nopTranscriber{}
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	// `key` says whose key it is — "own", or "shared" when it is the model's —
	// and never what it is.
	logrus.WithFields(logrus.Fields{
		"provider": name, "base_url": p.baseURL, "model": p.model, "key": keySource,
	}).Info("speech enabled")
	return &compatTranscriber{
		baseURL: p.baseURL,
		model:   p.model,
		apiKey:  key,
		client:  newClient(c.Timeout),
	}
}

// resolveKey is the key a client sends, and whose it is: its own, or — when it
// has none — a key the deployment holds for sharedBaseURL, if and only if
// baseURL is on that same host. The source is "own", "shared", or "" for none.
func resolveKey(own, shared, sharedBaseURL, baseURL string) (key, source string) {
	if k := strings.TrimSpace(own); k != "" {
		return k, "own"
	}
	if k := strings.TrimSpace(shared); k != "" && sameHost(baseURL, sharedBaseURL) {
		return k, "shared"
	}
	return "", ""
}

// sameHost reports whether two base URLs address the same host and port. An
// empty URL, one with no scheme, or one that will not parse matches nothing:
// the question is whether a key may be sent there, and "I cannot tell" is no.
func sameHost(a, b string) bool {
	ha, hb := hostOf(a), hostOf(b)
	return ha != "" && ha == hb
}

func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// container is one audio format: the extension a provider is told, and the
// bytes a file of it starts with.
type container struct {
	ext   string
	magic func(head []byte) bool
}

var (
	webm = container{"webm", func(h []byte) bool { return bytes.HasPrefix(h, []byte{0x1A, 0x45, 0xDF, 0xA3}) }}
	ogg  = container{"ogg", func(h []byte) bool { return bytes.HasPrefix(h, []byte("OggS")) }}
	mp4  = container{"m4a", func(h []byte) bool { return len(h) >= 8 && string(h[4:8]) == "ftyp" }}
	mp3  = container{"mp3", func(h []byte) bool {
		return bytes.HasPrefix(h, []byte("ID3")) || (len(h) >= 2 && h[0] == 0xFF && h[1]&0xE0 == 0xE0)
	}}
	wav  = container{"wav", func(h []byte) bool { return len(h) >= 12 && string(h[0:4]) == "RIFF" && string(h[8:12]) == "WAVE" }}
	flac = container{"flac", func(h []byte) bool { return bytes.HasPrefix(h, []byte("fLaC")) }}
)

// acceptedTypes maps every media type the route takes to its container.
//
// The extension is not decoration: OpenAI's endpoint decides the format from
// the multipart filename, and a clip named without one is refused as
// unsupported however valid its bytes are. The list is what browsers'
// MediaRecorder writes — WebM/Opus from Chrome and Firefox, MP4/AAC from Safari,
// Ogg from older Firefox — plus the three formats a person uploading a file by
// hand is likely to hold.
var acceptedTypes = map[string]container{
	"audio/webm":  webm,
	"audio/ogg":   ogg,
	"audio/mp4":   mp4,
	"audio/x-m4a": mp4,
	"audio/mpeg":  mp3,
	"audio/wav":   wav,
	"audio/x-wav": wav,
	"audio/flac":  flac,
}

// Accept decides whether a clip may be sent to a provider, before a byte is.
// It normalises the declared type — dropping parameters such as MediaRecorder's
// `;codecs=opus` — and requires the file to start the way that type starts.
//
// Both halves, because each alone admits something. A declared type alone
// stores whatever bytes arrived under an audio name, in a bucket a person later
// plays back from; the bytes alone would accept a WebM declared as MP3, which a
// provider decoding by the name it was told refuses — after the upload, and on
// some providers after the charge.
func Accept(declared string, head []byte) (mediaType, ext string, ok bool) {
	mt, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return "", "", false
	}
	c, known := acceptedTypes[mt]
	if !known || !c.magic(head) {
		return "", "", false
	}
	return mt, c.ext, true
}

// extensionFor is Accept's lookup without the bytes, for the client, which is
// handed a type Accept already checked.
func extensionFor(mediaType string) (string, bool) {
	c, ok := acceptedTypes[mediaType]
	return c.ext, ok
}

// NormalizeLanguage accepts an ISO-639-1 code — two letters, any case, with a
// region suffix dropped ("id-ID" → "id") — and refuses anything else. Both
// providers take exactly that shape, and they refuse a three-letter code or a
// language name after the upload rather than before it.
func NormalizeLanguage(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		s = s[:i]
	}
	if len(s) != 2 || s[0] < 'a' || s[0] > 'z' || s[1] < 'a' || s[1] > 'z' {
		return "", false
	}
	return s, true
}
