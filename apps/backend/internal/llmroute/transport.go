// Package llmroute sets OpenRouter's per-request provider routing preferences
// on outbound inference requests.
//
// OpenRouter accepts a `provider` object in the JSON body that narrows which
// upstream endpoints may serve the request
// (https://openrouter.ai/docs/features/provider-routing). This package owns
// that object, and there are two preferences in it:
//
//   - `zdr: true` restricts routing to endpoints whose operator stores the
//     payload for no period of time and may not train on it
//     (https://openrouter.ai/docs/guides/features/zdr). Account- and
//     guardrail-level ZDR settings OR with this flag, so sending it can only
//     narrow routing, never widen it: a deployment that already enforces ZDR in
//     the OpenRouter dashboard loses nothing by also sending it here, and one
//     that does not stops depending on a checkbox nobody in this repo can see.
//
//   - `ignore: [...]` removes named providers from the pool. It exists because
//     "OpenRouter serves this model" is not the same claim as "every endpoint
//     serving this model behaves the same", and one that does not can end a
//     turn silently. See BrokenToolCallProviders.
//
// Both are applied in an http.RoundTripper rather than at the call sites
// because every LLM tier here (primary, light, classifier, per-tenant
// overrides) reaches the wire through one openai-go client, and agent-sdk-go
// exposes no hook for extra body fields. A transport catches every caller,
// including the ones added after this file.
//
// The package was called llmzdr until the deny-list joined it. The rename
// records that the body's `provider` object had never been about ZDR alone —
// it was always the one seam this repo has for telling OpenRouter where a
// prompt may go.
package llmroute

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
)

// openRouterHost is the only host that understands `provider`. OpenAI itself
// rejects unknown top-level body fields with a 400, so the object must not be
// sprayed at every gateway an operator might configure.
const openRouterHost = "openrouter.ai"

// inferencePaths are the OpenRouter endpoints that carry a prompt and accept
// provider preferences. The management endpoints (/api/v1/keys,
// /api/v1/credits) send no prompt, so routing preferences are meaningless
// there and an unknown field would only earn a 400.
var inferencePaths = []string{"/chat/completions", "/completions", "/responses"}

// BrokenToolCallProviders are the OpenRouter provider slugs measured to answer
// a tool-calling request with the model's *native* tool-call syntax as
// assistant text, instead of the structured `tool_calls` the OpenAI wire
// format defines.
//
// This is not a quality preference. A turn routed to one of these ends after a
// single iteration with no tool ever running: agent-sdk-go's streaming loop
// breaks when the assistant message carries no tool calls, so the model's
// reasoning is persisted as the answer and the user is shown a paragraph that
// stops mid-thought. It is the failure mode `guardrails.CheckToolCallLeak`
// exists to name.
//
// Measured 2026-09-11 against `moonshotai/kimi-k2.6`, the configured primary
// model, by sending the backend's own request shape to seventeen of the model's
// twenty-one endpoints, pinned in turn. Fifteen returned `finish_reason:
// tool_calls` with structured deltas and one errored. `decart` returned
// `finish_reason: stop`, zero tool-call deltas, and `functions.get_schema:0{"source_id":…}` sitting in the content —
// identically with `stream` on and off, so the blocking fallback in
// ChatRunner.Run does not rescue it either. It advertises `"tools"` in its
// `supported_parameters`, which is why OpenRouter's own `require_parameters`
// preference does not filter it out, and it is the cheapest completion price
// on the model, which is why default price-first routing picked it often
// enough to look like an intermittent product bug.
//
// A slug leaves this list when somebody re-measures it, not when the provider
// says it is fixed. docs/coverage/provider-routing.md carries the probe and
// the full per-endpoint table this line summarises.
var BrokenToolCallProviders = []string{"decart"}

// Options is what this deployment wants OpenRouter's routing narrowed to.
type Options struct {
	// ZDR sends `provider.zdr: true`. Off by default; see config.LLMZDR for
	// why that is an operator's decision rather than this repo's.
	ZDR bool
	// Ignore sends `provider.ignore`, a list of provider slugs. The only
	// caller in the product passes BrokenToolCallProviders; tests pass their
	// own to prove the merge.
	Ignore []string
}

func (o Options) empty() bool { return !o.ZDR && len(o.Ignore) == 0 }

// Transport injects provider preferences into OpenRouter inference requests
// and forwards everything else untouched.
type Transport struct {
	Base http.RoundTripper
	Opts Options
}

// New returns a Transport wrapping base; a nil base means http.DefaultTransport.
func New(base http.RoundTripper, opts Options) *Transport {
	return &Transport{Base: base, Opts: opts}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.Opts.empty() || !appliesTo(req) {
		return base.RoundTrip(req)
	}
	body, err := readBody(req)
	if err != nil {
		return nil, fmt.Errorf("llmroute: read request body: %w", err)
	}
	patched, err := patch(body, t.Opts)
	if err != nil {
		// **The two preferences fail in opposite directions, on purpose.**
		//
		// ZDR fails closed. A body this layer cannot parse is a body it cannot
		// prove carries the flag, and sending it anyway would put the prompt on
		// a provider free to retain it — the one outcome the operator switched
		// ZDR on to prevent. A 500 here is loud and local.
		if t.Opts.ZDR {
			return nil, fmt.Errorf("llmroute: patch request body: %w", err)
		}
		// The deny-list fails open. It is on by default and in front of every
		// request in the product, so refusing to send an unparseable body would
		// convert a JSON hiccup into a total outage. What it is protecting
		// against costs one turn, and that turn is caught downstream by
		// guardrails.CheckToolCallLeak, which turns it into a sentence the user
		// can act on. The two halves of this fix cover each other; neither is
		// asked to be the only one.
		logrus.WithError(err).Warn("could not set OpenRouter provider preferences; sending the request unrouted")
		return base.RoundTrip(req)
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

// patch merges the configured preferences into an OpenAI-shaped request body.
//
// Every field is held as json.RawMessage and written back verbatim. Round
// tripping through map[string]any instead would decode numbers as float64 and
// re-encode them in scientific notation, so a large `seed` would reach the
// provider as a different number than the caller sent.
func patch(body []byte, opts Options) ([]byte, error) {
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
	if opts.ZDR {
		prefs["zdr"] = json.RawMessage("true")
	}
	if len(opts.Ignore) > 0 {
		merged, err := mergeIgnore(prefs["ignore"], opts.Ignore)
		if err != nil {
			return nil, err
		}
		prefs["ignore"] = merged
	}
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

// mergeIgnore unions a caller's own ignore list with this deployment's, in the
// caller's order first. Union rather than replace for the same reason patch
// merges the `provider` object at all: a caller that had excluded a provider
// meant it, and a layer that silently re-enabled one would be routing a prompt
// somewhere it was told not to. Deduplicated so a slug named twice is sent
// once.
func mergeIgnore(existing json.RawMessage, add []string) (json.RawMessage, error) {
	var list []string
	if len(existing) > 0 && !isJSONNull(existing) {
		if err := json.Unmarshal(existing, &list); err != nil {
			return nil, fmt.Errorf("decode provider ignore list: %w", err)
		}
	}
	seen := make(map[string]bool, len(list)+len(add))
	out := make([]string, 0, len(list)+len(add))
	for _, s := range append(list, add...) {
		slug := strings.TrimSpace(s)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode provider ignore list: %w", err)
	}
	return encoded, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
