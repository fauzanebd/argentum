package llmroute

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureTransport stands in for the network: it records the request the
// rewriter produced and answers 200. Tests cannot use httptest here — the
// rewrite is gated on the OpenRouter hostname, and a test server is 127.0.0.1.
type captureTransport struct {
	req  *http.Request
	body []byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.req = req
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		c.body = b
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

// zdrOnly and denyOnly are the two configurations with different failure
// directions; production runs denyOnly, or both when LLM_ZDR is set.
var (
	zdrOnly  = Options{ZDR: true}
	denyOnly = Options{Ignore: BrokenToolCallProviders}
)

func post(t *testing.T, url, contentType, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	return req
}

func roundTrip(t *testing.T, opts Options, req *http.Request) *captureTransport {
	t.Helper()
	rt := &captureTransport{}
	resp, err := New(rt, opts).RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	_ = resp.Body.Close()
	return rt
}

func TestInjectsZDRPreference(t *testing.T) {
	req := post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json",
		`{"model":"anthropic/claude-haiku-4.5","messages":[{"role":"user","content":"hi"}],"seed":9007199254740993}`)
	rt := roundTrip(t, zdrOnly, req)

	var got map[string]json.RawMessage
	if err := json.Unmarshal(rt.body, &got); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	var prefs struct {
		ZDR bool `json:"zdr"`
	}
	if err := json.Unmarshal(got["provider"], &prefs); err != nil {
		t.Fatalf("decode provider preferences: %v", err)
	}
	if !prefs.ZDR {
		t.Errorf("provider.zdr = false; want true (body: %s)", rt.body)
	}
	// A seed past 2^53 survives only because untouched fields are copied as
	// raw bytes; a map[string]any round trip would return 9007199254740992.
	if string(got["seed"]) != "9007199254740993" {
		t.Errorf("seed = %s; want 9007199254740993 — unrelated fields must round trip verbatim", got["seed"])
	}
	if rt.req.ContentLength != int64(len(rt.body)) {
		t.Errorf("ContentLength = %d; want %d", rt.req.ContentLength, len(rt.body))
	}
}

// The deny-list is the half that is on by default, so this is the shape almost
// every request in the product actually carries.
func TestInjectsIgnoreList(t *testing.T) {
	rt := roundTrip(t, denyOnly, post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json",
		`{"model":"moonshotai/kimi-k2.6","messages":[{"role":"user","content":"hi"}]}`))

	var got struct {
		Provider struct {
			Ignore []string `json:"ignore"`
			ZDR    *bool    `json:"zdr"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(rt.body, &got); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	if len(got.Provider.Ignore) != 1 || got.Provider.Ignore[0] != "decart" {
		t.Errorf("provider.ignore = %v; want [decart]", got.Provider.Ignore)
	}
	// ZDR is a separate operator decision. A deployment that left it off must
	// not have it switched on by the deny-list travelling in the same object.
	if got.Provider.ZDR != nil {
		t.Errorf("provider.zdr = %v; want absent when Options.ZDR is false", *got.Provider.ZDR)
	}
}

func TestMergesWithExistingProviderPreferences(t *testing.T) {
	req := post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json",
		`{"model":"m","provider":{"order":["anthropic"],"allow_fallbacks":false}}`)
	rt := roundTrip(t, Options{ZDR: true, Ignore: BrokenToolCallProviders}, req)

	var got struct {
		Provider struct {
			ZDR            bool     `json:"zdr"`
			Ignore         []string `json:"ignore"`
			Order          []string `json:"order"`
			AllowFallbacks *bool    `json:"allow_fallbacks"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(rt.body, &got); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	if !got.Provider.ZDR {
		t.Errorf("provider.zdr = false; want true")
	}
	if len(got.Provider.Ignore) != 1 || got.Provider.Ignore[0] != "decart" {
		t.Errorf("provider.ignore = %v; want [decart]", got.Provider.Ignore)
	}
	if len(got.Provider.Order) != 1 || got.Provider.Order[0] != "anthropic" {
		t.Errorf("provider.order = %v; want [anthropic] — existing routing preferences must survive", got.Provider.Order)
	}
	if got.Provider.AllowFallbacks == nil || *got.Provider.AllowFallbacks {
		t.Errorf("provider.allow_fallbacks = %v; want false", got.Provider.AllowFallbacks)
	}
}

// A caller that had already excluded a provider meant it. Unioning rather than
// replacing is the difference between narrowing routing and quietly re-opening
// an endpoint somebody switched off.
func TestUnionsWithAnExistingIgnoreList(t *testing.T) {
	req := post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json",
		`{"model":"m","provider":{"ignore":["chutes","decart"]}}`)
	rt := roundTrip(t, denyOnly, req)

	var got struct {
		Provider struct {
			Ignore []string `json:"ignore"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(rt.body, &got); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	want := []string{"chutes", "decart"}
	if len(got.Provider.Ignore) != len(want) {
		t.Fatalf("provider.ignore = %v; want %v — caller's entries kept, ours deduplicated", got.Provider.Ignore, want)
	}
	for i, slug := range want {
		if got.Provider.Ignore[i] != slug {
			t.Errorf("provider.ignore[%d] = %q; want %q", i, got.Provider.Ignore[i], slug)
		}
	}
}

func TestGetBodyReplaysRewrittenBody(t *testing.T) {
	req := post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json", `{"model":"m"}`)
	rt := roundTrip(t, denyOnly, req)

	rc, err := rt.req.GetBody()
	if err != nil {
		t.Fatalf("get body: %v", err)
	}
	defer func() { _ = rc.Close() }()
	replayed, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read replayed body: %v", err)
	}
	if !bytes.Equal(replayed, rt.body) {
		t.Errorf("GetBody() = %s; want %s — a redirect or retry must resend the routed body", replayed, rt.body)
	}
}

func TestPassesThroughRequestsItCannotOrMustNotRewrite(t *testing.T) {
	tests := []struct {
		name, url, contentType, body string
	}{
		{"other host", "https://api.openai.com/v1/chat/completions", "application/json", `{"model":"m"}`},
		{"lookalike host", "https://openrouter.ai.evil.example/v1/chat/completions", "application/json", `{"model":"m"}`},
		{"non-inference path", "https://openrouter.ai/api/v1/keys", "application/json", `{"name":"k"}`},
		{"non-JSON body", "https://openrouter.ai/api/v1/chat/completions", "multipart/form-data; boundary=x", `--x--`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := roundTrip(t, Options{ZDR: true, Ignore: BrokenToolCallProviders}, post(t, tt.url, tt.contentType, tt.body))
			if string(rt.body) != tt.body {
				t.Errorf("body = %s; want %s unchanged", rt.body, tt.body)
			}
		})
	}
}

// An operator who cleared LLM_PROVIDER_IGNORE and left LLM_ZDR off wants no
// `provider` object at all, not an empty one.
func TestNoPreferencesRewritesNothing(t *testing.T) {
	body := `{"model":"m"}`
	rt := roundTrip(t, Options{}, post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json", body))
	if string(rt.body) != body {
		t.Errorf("body = %s; want %s unchanged", rt.body, body)
	}
}

func TestSubdomainAndTrailingSlashPathStillRewrite(t *testing.T) {
	rt := roundTrip(t, zdrOnly, post(t, "https://gateway.openrouter.ai/api/v1/chat/completions/", "application/json; charset=utf-8", `{"model":"m"}`))
	if !bytes.Contains(rt.body, []byte(`"zdr":true`)) {
		t.Errorf("body = %s; want provider.zdr injected", rt.body)
	}
}

func TestUnparseableBodyFailsClosedForZDR(t *testing.T) {
	for _, body := range []string{`{"model":`, `["not","an","object"]`, `null`} {
		rt := &captureTransport{}
		_, err := New(rt, zdrOnly).RoundTrip(post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json", body))
		if err == nil {
			t.Errorf("body %s: RoundTrip returned nil error; want failure rather than an unprotected request", body)
		}
		if rt.req != nil {
			t.Errorf("body %s: request reached the network without a ZDR flag", body)
		}
	}
}

// The opposite direction, and deliberately so: the deny-list is in front of
// every request in the product, so a body it cannot parse must cost one
// unrouted turn rather than the whole deployment. guardrails.CheckToolCallLeak
// catches what gets through.
func TestUnparseableBodyFailsOpenForTheDenyList(t *testing.T) {
	const body = `{"model":`
	rt := &captureTransport{}
	resp, err := New(rt, denyOnly).RoundTrip(post(t, "https://openrouter.ai/api/v1/chat/completions", "application/json", body))
	if err != nil {
		t.Fatalf("round trip: %v; want the request sent unrouted rather than failed", err)
	}
	_ = resp.Body.Close()
	if string(rt.body) != body {
		t.Errorf("body = %s; want %s sent through untouched", rt.body, body)
	}
}

func TestTargetsOpenRouter(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"https://openrouter.ai/api/v1", true},
		{"  https://OpenRouter.ai/api/v1/  ", true},
		{"openrouter.ai/api/v1", true},
		{"https://gateway.openrouter.ai/v1", true},
		{"https://api.openai.com/v1", false},
		{"https://openrouter.ai.evil.example/v1", false},
		{"://nonsense", false},
	}
	for _, tt := range tests {
		if got := TargetsOpenRouter(tt.in); got != tt.want {
			t.Errorf("TargetsOpenRouter(%q) = %v; want %v", tt.in, got, tt.want)
		}
	}
}
