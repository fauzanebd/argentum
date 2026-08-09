package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/fauzanebd/argentum/internal/tracing"
)

// recordSpans installs a real SDK tracer provider for the duration of a test
// and hands back the exporter holding what was produced.
//
// A recording provider is the point. `tracing.Init` installs nothing when
// OTEL_EXPORTER_OTLP_ENDPOINT is unset, so every span in the tree is
// non-recording by default and a test that only asserted "it did not panic"
// would have passed against the defect this file exists for — a process that
// starts no spans and one whose spans go nowhere look identical from inside.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	// The propagator as well as the provider, because they are two halves of
	// one thing: Inject writes through the propagator and returns nothing
	// without one, so a test that installed only the provider would report the
	// exact symptom this file is about for an entirely different reason.
	// tracing.Init installs both together.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		otel.SetTextMapPropagator(prevProp)
		_ = tp.Shutdown(t.Context())
	})
	return rec
}

func routerWithTracing(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1")
	g.Use(RequestID())
	g.Use(Tracing())
	g.POST("/reports/:id", handler)
	return r
}

// The defect, stated as a test: a request handler must be able to see a span
// on its context. `tracing.Inject` reads exactly that, and returns nil when
// there is nothing there — which is how a payload crossed the queue carrying
// no traceparent and the worker started a second, unrelated trace for the same
// turn.
func TestARequestCarriesATraceTheQueueCanInherit(t *testing.T) {
	recordSpans(t)

	var carrier map[string]string
	r := routerWithTracing(func(c *gin.Context) {
		carrier = tracing.Inject(c.Request.Context())
		c.Status(http.StatusAccepted)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/reports/rep-1", nil))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	if len(carrier) == 0 {
		t.Fatal("the handler had no span to inject; a queued job would start its own trace")
	}
	if _, ok := carrier["traceparent"]; !ok {
		t.Errorf("carrier has no traceparent: %v", carrier)
	}
}

// The span is named for the route template, not the path. One span name per
// report id is a trace backend's index turned into a list of every id that has
// ever been asked for.
func TestTheSpanIsNamedForTheRouteNotThePath(t *testing.T) {
	rec := recordSpans(t)

	r := routerWithTracing(func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/reports/rep-1", nil))

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans ended, want 1", len(spans))
	}
	if got := spans[0].Name(); got != "POST /v1/reports/:id" {
		t.Errorf("span name = %q, want the route template", got)
	}
	var status, reqID bool
	for _, a := range spans[0].Attributes() {
		switch a.Key {
		case "http.response.status_code":
			status = a.Value.AsInt64() == http.StatusOK
		case "argentum.request_id":
			// The one string an integrator has when they report a problem.
			reqID = a.Value.AsString() != ""
		}
	}
	if !status {
		t.Error("the span does not carry the status the caller was answered with")
	}
	if !reqID {
		t.Error("the span does not carry the request id, so an id in a ticket cannot find it")
	}
}

// An unmatched request is one bucket, not one span name per URL somebody
// guessed at. A 404 flood must not be able to grow the index.
//
// Installed on the engine rather than on a group, because that is the only
// arrangement in which an unmatched request reaches this middleware at all —
// gin runs group handlers only for routes inside the group. `cmd/api` mounts
// it per group, so this branch is reachable only for a future caller; the
// fallback is kept because a span name derived from a raw path is the failure
// it prevents, and a nil route is the shape that produces one.
func TestAnUnmatchedRequestIsOneBucket(t *testing.T) {
	rec := recordSpans(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Tracing())
	r.POST("/v1/reports", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{"/v1/nope", "/v1/also-nope"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	}

	names := map[string]int{}
	for _, s := range rec.Ended() {
		names[s.Name()]++
	}
	if len(names) != 1 || names["GET unmatched"] != 2 {
		t.Errorf("unmatched requests produced %v, want one bucket of two", names)
	}
}
