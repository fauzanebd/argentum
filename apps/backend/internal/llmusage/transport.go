package llmusage

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// maxSSELineBytes bounds the partial-line buffer. A usage chunk is a few
// hundred bytes; anything past this is a malformed or hostile stream, so the
// buffer is dropped rather than grown. Losing usage is a warning (MeteredLLM
// logs it); an unbounded buffer on a long stream is an outage.
const maxSSELineBytes = 1 << 20

// Transport is an http.RoundTripper that reads token usage out of
// OpenAI-compatible SSE responses and adds it to the Collector carried by the
// request context. Requests without a collector, and non-SSE responses, pass
// through untouched — non-streaming calls are already metered from the SDK's
// LLMResponse.Usage, and double-counting is worse than the bug being fixed.
//
// It also reports what the provider says it served to a ServingSink, when the
// context carries one (T-Q15). That is off unless a caller installs a sink,
// and only streaming responses are read: the non-streaming light-model calls
// carry no collector either, so a run's *scored* model is exactly the set this
// sees.
type Transport struct {
	Base http.RoundTripper
}

// NewClient returns an *http.Client suitable for handing to
// openai-go's option.WithHTTPClient. No Timeout is set: streaming responses
// are long-lived and a client timeout would truncate them mid-answer.
func NewClient(base http.RoundTripper) *http.Client {
	return &http.Client{Transport: &Transport{Base: base}}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	col := CollectorFrom(req.Context())
	sink := ServingSinkFrom(req.Context())
	if (col == nil && sink == nil) || resp.Body == nil {
		return resp, nil
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return resp, nil
	}
	resp.Body = &tapReader{rc: resp.Body, col: col, sink: sink}
	return resp, nil
}

// tapReader forwards the response body byte-for-byte while scanning it for SSE
// `data:` frames carrying a usage object. It never buffers the whole body.
type tapReader struct {
	rc   io.ReadCloser
	col  *Collector
	sink *ServingSink

	partial []byte // bytes after the last newline seen
	last    Usage  // last usage payload observed in this response
	found   bool
	flushed bool

	// serving is the identity this response reported. One response is one
	// model on one provider, so it is recorded once at flush rather than per
	// frame — a hundred-chunk answer is one routing decision, and counting it
	// a hundred times would make "how many responses did this model serve"
	// unreadable.
	serving Serving
}

func (t *tapReader) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.scan(p[:n])
	}
	if err == io.EOF {
		t.flush()
	}
	return n, err
}

func (t *tapReader) Close() error {
	t.flush()
	return t.rc.Close()
}

// flush publishes the response's usage exactly once. The LAST usage payload
// wins rather than the sum: the OpenAI streaming contract emits one final
// usage chunk holding the totals for the request, and gateways that repeat a
// cumulative usage object on every chunk would otherwise be multiplied. Across
// separate HTTP requests (tool-calling iterations) the collector still sums.
func (t *tapReader) flush() {
	if t.flushed {
		return
	}
	t.flushed = true
	// The serving is published even when no usage arrived: a response that
	// named its model and reported no tokens is a billing hole with a known
	// culprit, and dropping the identity would hide which route produced it.
	t.sink.Observe(t.serving)
	if !t.found {
		return
	}
	t.col.Add(t.last)
}

func (t *tapReader) scan(b []byte) {
	t.partial = append(t.partial, b...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		line := t.partial[:i]
		t.partial = t.partial[i+1:]
		t.consumeLine(line)
	}
	if len(t.partial) > maxSSELineBytes {
		t.partial = t.partial[:0]
	}
}

func (t *tapReader) consumeLine(line []byte) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	var frame struct {
		Usage *rawUsage `json:"usage"`
		// What answered. OpenAI-compatible gateways repeat the model on every
		// chunk; OpenRouter adds the upstream it routed to. Both are read from
		// the first frame that names them and not overwritten, so a gateway
		// that omits the field on later chunks does not erase the identity.
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		return
	}
	if t.serving.Model == "" {
		t.serving.Model = strings.TrimSpace(frame.Model)
	}
	if t.serving.Provider == "" {
		t.serving.Provider = strings.TrimSpace(frame.Provider)
	}
	if frame.Usage == nil {
		return
	}
	u, ok := frame.Usage.normalize()
	if !ok {
		return
	}
	t.last = u
	t.found = true
}

// rawUsage covers the shapes an OpenAI-compatible gateway emits:
//   - OpenAI / OpenRouter: prompt_tokens, completion_tokens, and cached reads
//     under prompt_tokens_details.cached_tokens
//   - Anthropic-flavoured gateways proxied over the OpenAI wire format:
//     input_tokens / output_tokens plus cache_*_input_tokens
type rawUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// normalize converts a provider payload into cache-exclusive input tokens.
//
// The two wire formats disagree about what "input" means: OpenAI's
// prompt_tokens INCLUDES prompt_tokens_details.cached_tokens, Anthropic's
// input_tokens EXCLUDES its cache_*_input_tokens. RecordLLM prices cache reads
// separately at 0.10x, so a cached token left inside InputTokens would be
// billed twice — hence the subtraction on the OpenAI shape only.
func (r *rawUsage) normalize() (Usage, bool) {
	var u Usage
	switch {
	case r.PromptTokens > 0 || r.CompletionTokens > 0:
		u.InputTokens = r.PromptTokens
		u.OutputTokens = r.CompletionTokens
		if r.PromptTokensDetails != nil {
			u.CacheReadInputTokens = r.PromptTokensDetails.CachedTokens
		}
		if u.CacheReadInputTokens == 0 {
			u.CacheReadInputTokens = r.CacheReadInputTokens
		}
		u.CacheCreationInputTokens = r.CacheCreationInputTokens
		u.InputTokens -= u.CacheReadInputTokens + u.CacheCreationInputTokens
		if u.InputTokens < 0 {
			u.InputTokens = 0
		}
	default:
		u.InputTokens = r.InputTokens
		u.OutputTokens = r.OutputTokens
		u.CacheReadInputTokens = r.CacheReadInputTokens
		u.CacheCreationInputTokens = r.CacheCreationInputTokens
		if u.CacheReadInputTokens == 0 && r.PromptTokensDetails != nil {
			u.CacheReadInputTokens = r.PromptTokensDetails.CachedTokens
		}
	}
	if u.Empty() {
		return Usage{}, false
	}
	return u, true
}
