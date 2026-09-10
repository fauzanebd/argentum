package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/tracing"
)

// The Enqueuer stamps the trace, not its callers (T-17b).
//
// `ChatEnqueuer`, the scheduler and the watcher all produce `chat:run`. Three
// call sites is three chances to forget, and the one that forgets is invisible
// — the turn simply starts a new trace, which is exactly what every turn did
// before this ticket. So the stamping lives at the one place all three pass
// through, and this is the test that says so.

func enqueuerOn(t *testing.T) (*Enqueuer, asynq.RedisConnOpt) {
	t.Helper()
	srv := miniredis.RunT(t)
	opt := asynq.RedisClientOpt{Addr: srv.Addr()}
	e := NewEnqueuer(opt)
	t.Cleanup(func() { _ = e.Close() })
	return e, opt
}

func withRecordingTracer(t *testing.T) {
	t.Helper()
	prevTracer := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracer)
		otel.SetTextMapPropagator(prevProp)
	})
}

// payloadOf reads back what was actually queued. Asserting on the struct the
// caller passed would prove nothing — the stamping happens on a copy inside
// the enqueuer, and what the worker sees is the JSON.
func payloadOf(t *testing.T, opt asynq.RedisConnOpt, taskType, id string) map[string]any {
	t.Helper()
	insp := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = insp.Close() })
	info, err := insp.GetTaskInfo("default", id)
	if err != nil {
		// The video queue is the other lane a render can land in.
		info, err = insp.GetTaskInfo(QueueVideo, id)
	}
	if err != nil {
		t.Fatalf("read back %s: %v", taskType, err)
	}
	var out map[string]any
	if err := json.Unmarshal(info.Payload, &out); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return out
}

func TestChatRunCarriesTheTrace(t *testing.T) {
	withRecordingTracer(t)
	e, opt := enqueuerOn(t)

	ctx, span := tracing.Tracer().Start(context.Background(), "http.request")
	defer span.End()

	id, err := e.EnqueueChatRun(ctx, ChatRunPayload{CompanyID: "co-1", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	body := payloadOf(t, opt, TypeChatRun, id)
	carrier, _ := body["trace"].(map[string]any)
	if len(carrier) == 0 {
		t.Fatal("the queued turn carries no trace; the worker would start its own")
	}
	tp, _ := carrier["traceparent"].(string)
	if want := span.SpanContext().TraceID().String(); len(tp) == 0 || !contains(tp, want) {
		t.Fatalf("traceparent %q does not carry trace id %s", tp, want)
	}
	if _, ok := body["enqueued_at"].(string); !ok {
		t.Fatalf("no enqueued_at; the queue wait is unmeasurable: %v", body)
	}
}

// The render job takes the same stamp, and since T-V3 it is the longest task
// in the system — the one where the wait is most worth seeing.
func TestReportRenderCarriesTheTrace(t *testing.T) {
	withRecordingTracer(t)
	e, opt := enqueuerOn(t)

	ctx, span := tracing.Tracer().Start(context.Background(), "http.request")
	defer span.End()

	id, err := e.EnqueueReportRender(ctx, ReportRenderPayload{
		ReportID: "rep-1", CompanyID: "co-1", Spec: spec.Document{Format: "mp4"},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	body := payloadOf(t, opt, TypeReportRender, id)
	if carrier, _ := body["trace"].(map[string]any); len(carrier) == 0 {
		t.Fatal("the queued render carries no trace")
	}
	if _, ok := body["enqueued_at"].(string); !ok {
		t.Fatal("no enqueued_at on a job that can run for minutes")
	}
}

// With no collector — the ordinary deployment — nothing is stamped, so a
// payload does not grow a field for a trace nobody is collecting. The
// timestamp still travels: it costs one field and it is what makes queue depth
// legible the first time somebody turns tracing on.
func TestNoProviderMeansNoTraceField(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	e, opt := enqueuerOn(t)
	id, err := e.EnqueueChatRun(context.Background(), ChatRunPayload{CompanyID: "co-1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	body := payloadOf(t, opt, TypeChatRun, id)
	if _, ok := body["trace"]; ok {
		t.Errorf("a deployment with no collector stamped a trace: %v", body["trace"])
	}
	if _, ok := body["enqueued_at"].(string); !ok {
		t.Error("enqueued_at was dropped along with the trace")
	}
}

// A caller that set them keeps them. A retry re-enqueued by hand must not have
// its original trace and its original wait overwritten by the moment somebody
// pressed the button.
func TestAnExplicitStampIsNotOverwritten(t *testing.T) {
	withRecordingTracer(t)
	e, opt := enqueuerOn(t)

	when := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	ctx, span := tracing.Tracer().Start(context.Background(), "http.request")
	defer span.End()

	id, err := e.EnqueueChatRun(ctx, ChatRunPayload{
		CompanyID:  "co-1",
		Trace:      map[string]string{"traceparent": "kept"},
		EnqueuedAt: when,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	body := payloadOf(t, opt, TypeChatRun, id)
	carrier, _ := body["trace"].(map[string]any)
	if carrier["traceparent"] != "kept" {
		t.Errorf("trace = %v, want the caller's", carrier)
	}
	if got, _ := body["enqueued_at"].(string); got[:19] != when.Format("2006-01-02T15:04:05") {
		t.Errorf("enqueued_at = %s, want %s", got, when)
	}
}

// The second half of T-17b: webhook:deliver joins the trace too.
//
// It is the one task whose far end belongs to somebody else, so "we were slow"
// and "they were slow" are a support conversation rather than a curiosity, and
// the retry budget means the number worth reading is the wait rather than the
// POST.
func TestWebhookDeliveryCarriesTheTrace(t *testing.T) {
	withRecordingTracer(t)
	e, opt := enqueuerOn(t)

	ctx, span := tracing.Tracer().Start(context.Background(), "watcher.breached")
	defer span.End()

	if err := e.EnqueueWebhookDelivery(ctx, "del-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	body := onlyPending(t, opt)
	carrier, ok := body["trace"].(map[string]any)
	if !ok {
		t.Fatalf("the queued delivery carries no trace; the attempt would start its own: %v", body)
	}
	tp, _ := carrier["traceparent"].(string)
	if !contains(tp, span.SpanContext().TraceID().String()) {
		t.Errorf("traceparent = %q, want the enqueuing trace %s", tp, span.SpanContext().TraceID())
	}
	if _, ok := body["enqueued_at"].(string); !ok {
		t.Error("no enqueued_at; the trace would join the two processes without saying how long the row waited")
	}
}

// The decision the two Unique-guarded payloads are documented with, kept honest.
//
// asynq derives a unique task's dedup key as md5 of the payload
// (internal/base.UniqueKey), so adding a per-call traceparent to
// BusinessInferPayload or DocumentParsePayload would give every enqueue a
// distinct key and retire the window without touching the line that declares
// it. For document:parse that window is what stops a re-parse from paying for
// OCR twice. This test fails the moment somebody finishes "the second half" by
// stamping these two the way webhook:deliver is stamped above.
func TestADeduplicatedTaskIsNotSplitByItsTrace(t *testing.T) {
	withRecordingTracer(t)
	e, opt := enqueuerOn(t)

	// Two different traces, one logical job — a tenant clicking Re-scan twice,
	// where each click arrives on its own request span.
	for _, name := range []string{"http.request.first", "http.request.second"} {
		ctx, span := tracing.Tracer().Start(context.Background(), name)
		if err := e.EnqueueBusinessInference(ctx, "co-1", "conn-1", false); err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
		span.End()
	}

	insp := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = insp.Close() })
	pending, err := insp.ListPendingTasks("default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("queued %d business:infer tasks, want 1 — the dedup window is keyed on the payload, "+
			"so something in it now varies per call", len(pending))
	}
}

// onlyPending reads back the single task on the default queue, for the enqueue
// methods that return no id to look one up by.
func onlyPending(t *testing.T, opt asynq.RedisConnOpt) map[string]any {
	t.Helper()
	insp := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = insp.Close() })
	pending, err := insp.ListPendingTasks("default")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d tasks, want exactly 1", len(pending))
	}
	var out map[string]any
	if err := json.Unmarshal(pending[0].Payload, &out); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
