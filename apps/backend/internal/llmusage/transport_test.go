package llmusage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// openAIStream is the shape agent-sdk-go's OpenAI client receives when
// stream_options.include_usage is set: content chunks with a null usage field,
// then one final chunk carrying the totals, then [DONE].
const openAIStream = `data: {"id":"c1","choices":[{"delta":{"content":"Total "}}],"usage":null}

data: {"id":"c1","choices":[{"delta":{"content":"sales"}}],"usage":null}

data: {"id":"c1","choices":[],"usage":{"prompt_tokens":1200,"completion_tokens":300,"total_tokens":1500}}

data: [DONE]

`

func serveSSE(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
}

func drain(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := NewClient(nil).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestTransportRecordsUsageAndPassesBodyThrough(t *testing.T) {
	srv := serveSSE(t, openAIStream)
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	got := drain(t, ctx, srv.URL)

	if got != openAIStream {
		t.Fatalf("body was altered by the tap:\n%q", got)
	}
	u, events := col.Snapshot()
	if events != 1 {
		t.Fatalf("usage events = %d, want 1", events)
	}
	if u.InputTokens != 1200 || u.OutputTokens != 300 {
		t.Fatalf("usage = %+v, want in=1200 out=300", u)
	}
}

func TestTransportSubtractsCachedTokensFromPrompt(t *testing.T) {
	// OpenAI semantics: prompt_tokens INCLUDES cached tokens. RecordLLM prices
	// cache reads separately, so leaving them in InputTokens bills them twice.
	body := `data: {"usage":{"prompt_tokens":1000,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":800}}}

data: [DONE]

`
	srv := serveSSE(t, body)
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	drain(t, ctx, srv.URL)

	u, _ := col.Snapshot()
	if u.InputTokens != 200 || u.CacheReadInputTokens != 800 || u.OutputTokens != 50 {
		t.Fatalf("usage = %+v, want in=200 cacheRead=800 out=50", u)
	}
}

func TestTransportKeepsAnthropicShapeCacheExclusive(t *testing.T) {
	// Anthropic semantics over an OpenAI-compatible gateway: input_tokens
	// EXCLUDES cache tokens, so nothing may be subtracted.
	body := `data: {"usage":{"input_tokens":120,"output_tokens":40,"cache_creation_input_tokens":900,"cache_read_input_tokens":300}}

data: [DONE]

`
	srv := serveSSE(t, body)
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	drain(t, ctx, srv.URL)

	u, _ := col.Snapshot()
	if u.InputTokens != 120 || u.OutputTokens != 40 ||
		u.CacheCreationInputTokens != 900 || u.CacheReadInputTokens != 300 {
		t.Fatalf("usage = %+v, want in=120 out=40 create=900 read=300", u)
	}
}

func TestTransportTakesLastUsagePayloadPerResponse(t *testing.T) {
	// Gateways that repeat a cumulative usage object on every chunk must not
	// be summed within one response, or a long answer bills many times over.
	body := `data: {"usage":{"prompt_tokens":100,"completion_tokens":10}}

data: {"usage":{"prompt_tokens":100,"completion_tokens":25}}

data: {"usage":{"prompt_tokens":100,"completion_tokens":40}}

data: [DONE]

`
	srv := serveSSE(t, body)
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	drain(t, ctx, srv.URL)

	u, events := col.Snapshot()
	if events != 1 || u.InputTokens != 100 || u.OutputTokens != 40 {
		t.Fatalf("usage = %+v events = %d, want in=100 out=40 events=1", u, events)
	}
}

func TestTransportSumsAcrossRequests(t *testing.T) {
	// One agent turn = one GenerateWithToolsStream call = one HTTP request per
	// tool-calling iteration. Every iteration must be billed.
	srv := serveSSE(t, openAIStream)
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	drain(t, ctx, srv.URL)
	drain(t, ctx, srv.URL)
	drain(t, ctx, srv.URL)

	u, events := col.Snapshot()
	if events != 3 || u.InputTokens != 3600 || u.OutputTokens != 900 {
		t.Fatalf("usage = %+v events = %d, want in=3600 out=900 events=3", u, events)
	}
}

func TestTransportHandlesUsageSplitAcrossReads(t *testing.T) {
	// The tap parses incrementally; a usage frame that arrives in pieces (as
	// it does over a real chunked connection) must still be read.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		for _, piece := range []string{
			`data: {"usage":{"prompt_to`,
			`kens":77,"completion_tokens`,
			`":11}}` + "\n\n" + "data: [DONE]\n\n",
		} {
			_, _ = io.WriteString(w, piece)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	drain(t, ctx, srv.URL)

	u, _ := col.Snapshot()
	if u.InputTokens != 77 || u.OutputTokens != 11 {
		t.Fatalf("usage = %+v, want in=77 out=11", u)
	}
}

func TestTransportIgnoresNonStreamResponses(t *testing.T) {
	// Non-streaming calls are metered from the SDK's LLMResponse.Usage. Taping
	// them too would double-bill every guardrail and classifier call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":500,"completion_tokens":20}}`)
	}))
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	drain(t, ctx, srv.URL)

	if _, events := col.Snapshot(); events != 0 {
		t.Fatalf("usage events = %d, want 0 for a non-SSE response", events)
	}
}

func TestTransportWithoutCollectorIsPassThrough(t *testing.T) {
	srv := serveSSE(t, openAIStream)
	defer srv.Close()

	got := drain(t, context.Background(), srv.URL)
	if !strings.Contains(got, `"prompt_tokens":1200`) {
		t.Fatalf("body was altered without a collector:\n%q", got)
	}
}

func TestTransportFlushesUsageOnEarlyClose(t *testing.T) {
	// A caller that stops reading after the usage frame (or a stream the SDK
	// closes early) must still bill what the provider already reported.
	srv := serveSSE(t, openAIStream)
	defer srv.Close()

	ctx, col := WithCollector(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := NewClient(nil).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	buf := make([]byte, len(openAIStream)-4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = resp.Body.Close()

	if _, events := col.Snapshot(); events != 1 {
		t.Fatalf("usage events = %d after early close, want 1", events)
	}
}
