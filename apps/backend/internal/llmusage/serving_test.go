package llmusage

import (
	"context"
	"testing"
)

// openRouterStream is the shape OpenRouter returns: the model on every chunk
// and the upstream it routed to beside it. The provider field is the one worth
// having — the 2026-08-18 drift kept the same model id throughout.
const openRouterStream = `data: {"id":"gen-1","provider":"DeepInfra","model":"deepseek/deepseek-v3.2","choices":[{"delta":{"content":"Total "}}],"usage":null}

data: {"id":"gen-1","provider":"DeepInfra","model":"deepseek/deepseek-v3.2","choices":[{"delta":{"content":"sales"}}],"usage":null}

data: {"id":"gen-1","provider":"DeepInfra","model":"deepseek/deepseek-v3.2","choices":[],"usage":{"prompt_tokens":900,"completion_tokens":120}}

data: [DONE]

`

// noUsageStream names a model and reports no tokens at all. It is the shape
// that must still be recorded: a route that answers for free is the C-2 shape,
// and the identity is how anyone finds which route did it.
const noUsageStream = `data: {"id":"gen-2","provider":"Fireworks","model":"moonshotai/kimi-k2.6","choices":[{"delta":{"content":"hi"}}]}

data: [DONE]

`

func TestTransportRecordsWhatTheProviderSaysItServed(t *testing.T) {
	srv := serveSSE(t, openRouterStream)
	defer srv.Close()

	sink := &ServingSink{}
	ctx, col := WithCollector(context.Background())
	ctx = WithServingSink(ctx, sink)

	if got := drain(t, ctx, srv.URL); got != openRouterStream {
		t.Fatalf("body was altered by the tap:\n%q", got)
	}

	observed := sink.Observed()
	if len(observed) != 1 {
		t.Fatalf("observed = %+v, want exactly one serving", observed)
	}
	if observed[0].Model != "deepseek/deepseek-v3.2" || observed[0].Provider != "DeepInfra" {
		t.Fatalf("serving = %+v, want deepseek/deepseek-v3.2 via DeepInfra", observed[0].Serving)
	}
	// One response is one routing decision, however many chunks carried it.
	if observed[0].Responses != 1 {
		t.Fatalf("responses = %d, want 1 — the tap counted chunks, not responses", observed[0].Responses)
	}
	// The usage path must be untouched by any of this.
	if u, events := col.Snapshot(); events != 1 || u.InputTokens != 900 || u.OutputTokens != 120 {
		t.Fatalf("usage = %+v events=%d, want in=900 out=120 events=1", u, events)
	}
}

func TestServingIsRecordedEvenWhenNoUsageArrives(t *testing.T) {
	srv := serveSSE(t, noUsageStream)
	defer srv.Close()

	sink := &ServingSink{}
	ctx, col := WithCollector(context.Background())
	ctx = WithServingSink(ctx, sink)
	drain(t, ctx, srv.URL)

	if _, events := col.Snapshot(); events != 0 {
		t.Fatalf("usage events = %d, want 0 — this stream reports none", events)
	}
	observed := sink.Observed()
	if len(observed) != 1 || observed[0].Model != "moonshotai/kimi-k2.6" {
		t.Fatalf("observed = %+v, want the model named even with no usage", observed)
	}
}

func TestSinkWorksWithoutACollector(t *testing.T) {
	srv := serveSSE(t, openRouterStream)
	defer srv.Close()

	// A caller may want the identity and not the tokens. Before T-Q15 the
	// RoundTripper returned early unless a collector was present, so this
	// would have observed nothing.
	sink := &ServingSink{}
	ctx := WithServingSink(context.Background(), sink)
	drain(t, ctx, srv.URL)

	if got := sink.Observed(); len(got) != 1 {
		t.Fatalf("observed = %+v, want one serving with no collector installed", got)
	}
}

func TestTwoRoutesInOneRunAreTwoRows(t *testing.T) {
	first := serveSSE(t, openRouterStream)
	defer first.Close()
	second := serveSSE(t, noUsageStream)
	defer second.Close()

	sink := &ServingSink{}
	ctx := WithServingSink(context.Background(), sink)
	drain(t, ctx, first.URL)
	drain(t, ctx, first.URL)
	drain(t, ctx, second.URL)

	observed := sink.Observed()
	if len(observed) != 2 {
		t.Fatalf("observed = %+v, want two distinct servings", observed)
	}
	// Most responses first: the route that answered most of the run leads.
	if observed[0].Model != "deepseek/deepseek-v3.2" || observed[0].Responses != 2 {
		t.Fatalf("first row = %+v, want deepseek with 2 responses", observed[0])
	}
	if observed[1].Model != "moonshotai/kimi-k2.6" || observed[1].Responses != 1 {
		t.Fatalf("second row = %+v, want kimi with 1 response", observed[1])
	}
}

func TestNilSinkAndEmptyServingAreNoOps(t *testing.T) {
	var s *ServingSink
	s.Observe(Serving{Model: "x"}) // must not panic
	if got := s.Observed(); got != nil {
		t.Fatalf("nil sink observed = %+v, want nil", got)
	}
	real := &ServingSink{}
	real.Observe(Serving{})
	if got := real.Observed(); len(got) != 0 {
		t.Fatalf("empty serving was recorded: %+v", got)
	}
}

func TestServingString(t *testing.T) {
	cases := []struct {
		in   Serving
		want string
	}{
		{Serving{Model: "a/b", Provider: "P"}, "a/b via P"},
		{Serving{Model: "a/b"}, "a/b"},
		{Serving{Provider: "P"}, "via P"},
		{Serving{}, "(unnamed)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}
