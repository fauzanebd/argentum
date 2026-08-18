// Package llmzdr enforces OpenRouter's Zero Data Retention (ZDR) routing on
// outbound inference requests.
//
// OpenRouter exposes ZDR as a per-request routing preference — `provider.zdr:
// true` in the JSON body — which restricts the request to endpoints whose
// operator stores the payload for no period of time and may not train on it
// (https://openrouter.ai/docs/guides/features/zdr). Account- and
// guardrail-level ZDR settings OR with this flag, so sending it can only
// narrow routing, never widen it: a deployment that already enforces ZDR in
// the OpenRouter dashboard loses nothing by also sending it here, and one that
// does not stops depending on a checkbox nobody in this repo can see.
//
// It is applied in an http.RoundTripper rather than at the call sites because
// every LLM tier here (primary, light, classifier, per-tenant overrides)
// reaches the wire through one openai-go client, and agent-sdk-go exposes no
// hook for extra body fields. A transport catches every caller, including the
// ones added after this file.
package llmzdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// openRouterHost is the only host that understands `provider`. OpenAI itself
// rejects unknown top-level body fields with a 400, so the flag must not be
// sprayed at every gateway an operator might configure.
const openRouterHost = "openrouter.ai"

// inferencePaths are the OpenRouter endpoints that carry a prompt and accept
// provider preferences. The management endpoints (/api/v1/keys,
// /api/v1/credits) send no prompt, so ZDR is meaningless there and an unknown
// field would only earn a 400.
var inferencePaths = []string{"/chat/completions", "/completions", "/responses"}

// Transport injects provider.zdr into OpenRouter inference requests and
// forwards everything else untouched.
type Transport struct {
	Base http.RoundTripper
}

// New returns a Transport wrapping base; a nil base means http.DefaultTransport.
func New(base http.RoundTripper) *Transport { return &Transport{Base: base} }

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if !appliesTo(req) {
		return base.RoundTrip(req)
	}
	body, err := readBody(req)
	if err != nil {
		return nil, fmt.Errorf("zdr: read request body: %w", err)
	}
	patched, err := patch(body)
	if err != nil {
		// Fail closed. A body this layer cannot parse is a body it cannot
		// prove carries the flag, and sending it anyway would put the prompt
		// on a provider free to retain it — the one outcome the operator
		// switched ZDR on to prevent. A 500 here is loud and local.
		return nil, fmt.Errorf("zdr: patch request body: %w", err)
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(patched))
	out.ContentLength = int64(len(patched))
	out.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(patched)), nil
	}
	return base.RoundTrip(out)
}

// TargetsOpenRouter reports whether a configured base URL addresses
// OpenRouter. Callers use it to warn when ZDR is switched on for an endpoint
// where the flag would be silently dropped. An empty or unparseable URL is
// "not OpenRouter" — the answer that produces the warning, because the
// question being asked is "can this be enforced" and "I cannot tell" is a no.
func TargetsOpenRouter(baseURL string) bool {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return isOpenRouterHost(u.Hostname())
}

func appliesTo(req *http.Request) bool {
	if req == nil || req.Body == nil || req.Method != http.MethodPost || req.URL == nil {
		return false
	}
	if !isOpenRouterHost(req.URL.Hostname()) {
		return false
	}
	if !isJSONContentType(req.Header.Get("Content-Type")) {
		return false
	}
	return isInferencePath(req.URL.Path)
}

func isOpenRouterHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return h == openRouterHost || strings.HasSuffix(h, "."+openRouterHost)
}

func isJSONContentType(ct string) bool {
	mediaType, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

func isInferencePath(path string) bool {
	p := strings.TrimRight(path, "/")
	for _, suffix := range inferencePaths {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

// readBody prefers req.GetBody so the caller's request stays replayable — the
// http.Client re-reads it on redirects and on HTTP/2 GOAWAY retries, and a
// transport that had drained it would resend an empty prompt.
func readBody(req *http.Request) ([]byte, error) {
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}
	defer func() { _ = req.Body.Close() }()
	return io.ReadAll(req.Body)
}

// patch merges provider.zdr = true into an OpenAI-shaped request body.
//
// Every field is held as json.RawMessage and written back verbatim. Round
// tripping through map[string]any instead would decode numbers as float64 and
// re-encode them in scientific notation, so a large `seed` would reach the
// provider as a different number than the caller sent.
func patch(body []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	if top == nil {
		return nil, fmt.Errorf("decode body: not a JSON object")
	}
	prefs := map[string]json.RawMessage{}
	if raw, ok := top["provider"]; ok && !isJSONNull(raw) {
		// Merge rather than replace: the caller may already be pinning an
		// order or an allow-list, and dropping that would reroute the request.
		if err := json.Unmarshal(raw, &prefs); err != nil {
			return nil, fmt.Errorf("decode provider preferences: %w", err)
		}
	}
	prefs["zdr"] = json.RawMessage("true")
	encoded, err := json.Marshal(prefs)
	if err != nil {
		return nil, fmt.Errorf("encode provider preferences: %w", err)
	}
	top["provider"] = encoded
	out, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return out, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
