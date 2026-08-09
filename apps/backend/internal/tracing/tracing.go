// Package tracing is the OpenTelemetry half of T-17: one span per agent turn,
// child spans for the work inside it, exported over OTLP when a collector is
// configured and costing nothing when one is not.
//
// The "costing nothing" is the load-bearing part. `OTEL_EXPORTER_OTLP_ENDPOINT`
// unset installs no provider, so `otel.Tracer(…).Start` returns a
// non-recording span — a struct copy and a context value, no allocation per
// attribute and no exporter goroutine. That is what makes it acceptable to
// instrument the turn path unconditionally rather than behind an `if`, and an
// `if` at every call site is how instrumentation ends up covering only the
// paths somebody remembered.
package tracing

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/sirupsen/logrus"
)

// ScopeName is the instrumentation scope every span in this product carries.
const ScopeName = "github.com/fauzanebd/argentum"

// Init installs the global tracer provider when a collector is configured, and
// returns a shutdown function that is safe to call either way.
//
// service names the process — "argentum-api", "argentum-worker" — because a
// trace crossing the queue is the thing this exists to show, and a span that
// cannot say which process it ran in is half a trace.
//
// The endpoint comes from `OTEL_EXPORTER_OTLP_ENDPOINT`, which is the variable
// every OTel SDK reads, so a deployment already exporting from another service
// needs no Argentum-specific setting. `OTEL_EXPORTER_OTLP_PROTOCOL` picks
// between `grpc` (the default) and `http/protobuf`.
func Init(ctx context.Context, service, version string) (shutdown func(context.Context) error, err error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		// No provider installed. otel's default is a no-op, which is exactly the
		// behaviour this product had before the package existed.
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := newExporter(ctx)
	if err != nil {
		// A collector that is down must not stop a process whose job is answering
		// questions. Logged loudly, and the turn path carries on unrecorded.
		logrus.WithError(err).WithField("endpoint", endpoint).
			Error("otel: exporter could not be built; traces are not being exported")
		return func(context.Context) error { return nil }, nil
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(resourceFor(service, version)),
	)
	otel.SetTracerProvider(provider)
	// Both propagators, because a trace can arrive from a tenant's own client
	// over `/v1` and we do not get to choose which convention they use.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	logrus.WithFields(logrus.Fields{"endpoint": endpoint, "service": service}).
		Info("otel tracing enabled")
	return provider.Shutdown, nil
}

func newExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	switch os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") {
	case "http/protobuf", "http":
		return otlptracehttp.New(ctx)
	case "", "grpc":
		return otlptracegrpc.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL %q; use grpc or http/protobuf",
			os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	}
}

// Tracer is the one tracer this product uses. A tracer per package would give
// each span a different scope for no gain: what an operator filters on is the
// span name and its attributes, not which file created it.
func Tracer() trace.Tracer { return otel.Tracer(ScopeName) }

// Turn starts the span an agent turn runs under, and is the parent of every
// span below.
//
// The attributes are the ones an operator filters by when a customer says "it
// was slow at three o'clock": whose turn, on which channel, in which thread.
// Never the message text — a span exported to a third-party collector is a copy
// of whatever is on it, and the question is asked about latency rather than
// content.
func Turn(ctx context.Context, companyID, threadID, channel string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "agent.turn", trace.WithAttributes(
		attribute.String("argentum.company_id", companyID),
		attribute.String("argentum.thread_id", threadID),
		attribute.String("argentum.channel", channel),
	))
}

// Tool starts the span for one tool call. Started in the audit decorator, which
// is where every tool call already passes — the same reason the audit row and
// the metric are written there.
func Tool(ctx context.Context, tool string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "agent.tool", trace.WithAttributes(
		attribute.String("argentum.tool", tool),
	))
}

// Step starts a span for a named phase inside a turn — guardrails, memory
// hydration, the embedding lookup. One function rather than one per phase,
// because the phases are a list that grows and a package of near-identical
// three-line functions is a package nobody updates.
func Step(ctx context.Context, name string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "agent."+name)
}

// Outcome labels a span with what a phase decided, for the phases where the
// interesting answer is not "did it error" — a guardrail that redacted a reply
// did its job, and a waterfall that cannot tell that from a clean pass is
// missing the reason the span was worth exporting.
func Outcome(span trace.Span, outcome string) {
	span.SetAttributes(attribute.String("argentum.outcome", outcome))
}

// Inject captures the current trace context as a carrier a queue payload can
// hold (T-17b).
//
// Without it the two halves of a turn are two traces. `cmd/api` opens a span
// for the HTTP request, the work happens in `cmd/worker` minutes later, and
// nothing connects them — so the one interval a slow turn is most often blamed
// on, the wait in the queue, is the interval no waterfall can show. The
// producer is the only place that knows the trace, and the payload is the only
// thing that crosses the gap.
//
// A map rather than a string: `traceparent` and `tracestate` are two headers,
// the composite propagator writes whichever it has, and a payload field named
// after one of them would quietly drop the other. Nil when no provider is
// installed, which is the ordinary deployment and costs a payload nothing.
func Inject(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return carrier
}

// Extract restores a trace context captured by Inject.
//
// An absent or unparseable carrier returns ctx unchanged, so a task queued
// before this field existed — or by a process with no tracer — starts its own
// trace exactly as it did before. There is no version of this that should fail
// a turn.
func Extract(ctx context.Context, carrier map[string]string) context.Context {
	if len(carrier) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(carrier))
}

// QueueWait records how long a task sat before a worker picked it up.
//
// It is an attribute rather than a span of its own: nothing happens during the
// wait, and a span with no work in it is a bar on a waterfall that invites
// somebody to look for the code that ran inside it. A zero or negative
// duration is dropped — the two processes have their own clocks, and a
// negative wait is a clock difference reported as a fact.
func QueueWait(span trace.Span, enqueuedAt time.Time) {
	if enqueuedAt.IsZero() {
		return
	}
	if wait := time.Since(enqueuedAt); wait > 0 {
		span.SetAttributes(attribute.Int64("argentum.queue_wait_ms", wait.Milliseconds()))
	}
}

// End closes a span, recording the error when there is one. Every caller ends
// with `defer tracing.End(span, err)` reading a named return, which is the one
// shape that cannot forget the error on an early return.
func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
	}
	span.End()
}

// resourceFor names the process in every span it exports. Built by hand rather
// than through resource.Merge with the default: the default pulls in host and
// process attributes that are noise in a container and a small disclosure in a
// trace exported to somebody else's collector.
func resourceFor(service, version string) *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(service),
		semconv.ServiceVersion(version),
	)
}
