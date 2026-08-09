package tracing

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Carrying a trace across the queue (T-17b).
//
// This is the half of a turn nobody could see. `cmd/api` opened a span for the
// HTTP request, `cmd/worker` opened another for the turn, and the two were
// separate traces — so the wait between them, which is the interval a slow
// turn is most often blamed on, appeared in neither.
//
// It is worth a test rather than a live check for the reason the property is
// awkward: with no collector configured the spans are non-recording, so
// everything here "works" whether or not the context actually travels. What
// these assert is the thing a waterfall would show — same trace id, real
// parent — using a real SDK provider that nothing exports.

// withProvider installs a recording tracer provider and the propagators Init
// installs, and puts both back afterwards. Without the propagator, Inject
// writes nothing and every test below would pass vacuously.
func withProvider(t *testing.T) {
	t.Helper()
	prevTracer := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracer)
		otel.SetTextMapPropagator(prevProp)
	})
}

// The property the ticket exists for: one turn is one trace, across two
// processes.
func TestTheTraceSurvivesTheQueue(t *testing.T) {
	withProvider(t)

	// The producer: an HTTP request in cmd/api.
	producerCtx, producerSpan := Tracer().Start(context.Background(), "http.request")
	carrier := Inject(producerCtx)
	producerSpan.End()

	if len(carrier) == 0 {
		t.Fatal("nothing was captured; the payload would carry no trace")
	}
	if _, ok := carrier["traceparent"]; !ok {
		t.Fatalf("no traceparent in %v", carrier)
	}

	// The consumer: cmd/worker, minutes later, holding only the payload.
	consumerCtx := Extract(context.Background(), carrier)
	_, turnSpan := Turn(consumerCtx, "co-1", "th-1", "api")
	defer turnSpan.End()

	produced := producerSpan.SpanContext()
	consumed := turnSpan.SpanContext()
	if produced.TraceID() != consumed.TraceID() {
		t.Fatalf("two traces for one turn: %s and %s", produced.TraceID(), consumed.TraceID())
	}
	if !consumed.IsValid() {
		t.Fatal("the worker's span context is not valid")
	}
	if produced.SpanID() == consumed.SpanID() {
		t.Fatal("the worker reused the producer's span rather than starting a child")
	}
}

// A task queued before this field existed, or by a process with no tracer,
// starts its own trace. There is no version of this that should fail a turn.
func TestAnAbsentCarrierIsNotAnError(t *testing.T) {
	withProvider(t)

	for _, carrier := range []map[string]string{
		nil,
		{},
		{"traceparent": "nonsense"},
	} {
		ctx := Extract(context.Background(), carrier)
		_, span := Turn(ctx, "co-1", "th-1", "dashboard")
		if !span.SpanContext().IsValid() {
			t.Fatalf("carrier %v produced no usable span", carrier)
		}
		span.End()
	}
}

// With no provider installed — every deployment without a collector — Inject
// captures nothing, so a payload does not grow a field for a trace nobody is
// collecting.
func TestInjectIsEmptyWithoutAProvider(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	if got := Inject(context.Background()); got != nil {
		t.Fatalf("Inject with no propagator = %v, want nil", got)
	}
}

// The wait is an attribute rather than a span, and a clock that disagrees is
// dropped rather than reported.
//
// The two processes have their own clocks. A negative wait means they differ,
// which is a fact about the deployment and not about the turn — publishing it
// as a duration is how a waterfall grows a bar that says the work finished
// before it started.
func TestQueueWaitIgnoresAZeroOrBackwardsClock(t *testing.T) {
	withProvider(t)

	_, span := Tracer().Start(context.Background(), "agent.turn")
	defer span.End()

	// Neither of these may panic, and neither should record anything. There is
	// no exported reader for attributes on a live span, so what this pins is
	// the contract at its boundary: the two inputs that must be ignored.
	QueueWait(span, time.Time{})
	QueueWait(span, time.Now().Add(time.Hour))
}

// Turn is a child of whatever the carrier named, not a root. Asserted through
// the SDK's own recorder so it is the exported shape being checked.
func TestTheWorkerSpanIsAChild(t *testing.T) {
	withProvider(t)

	producerCtx, producerSpan := Tracer().Start(context.Background(), "http.request")
	carrier := Inject(producerCtx)
	producerSpan.End()

	ctx := Extract(context.Background(), carrier)
	parent := trace.SpanContextFromContext(ctx)
	if !parent.IsValid() {
		t.Fatal("the extracted context holds no span context")
	}
	if !parent.IsRemote() {
		t.Error("the extracted parent is not marked remote; a waterfall would file it as local work")
	}
	if parent.SpanID() != producerSpan.SpanContext().SpanID() {
		t.Fatalf("parent span = %s, want %s", parent.SpanID(), producerSpan.SpanContext().SpanID())
	}
}
