package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/fauzanebd/argentum/internal/tracing"
)

// Enqueuer wraps *asynq.Client with typed helpers so handlers don't have
// to know about asynq directly. One Enqueuer is shared by the API process
// and is safe for concurrent use.
type Enqueuer struct {
	client *asynq.Client
}

// NewEnqueuer constructs an Enqueuer with the supplied Redis options.
func NewEnqueuer(opt asynq.RedisConnOpt) *Enqueuer {
	return &Enqueuer{client: asynq.NewClient(opt)}
}

// Close releases the underlying asynq client. Call from main on shutdown.
func (e *Enqueuer) Close() error {
	if e == nil || e.client == nil {
		return nil
	}
	return e.client.Close()
}

// EnqueueChatRun dispatches one chat turn to the worker pool. The returned
// id is the asynq task ID, useful for tracing but not required for the WS
// stream subscription (which is keyed by thread_id).
func (e *Enqueuer) EnqueueChatRun(ctx context.Context, p ChatRunPayload) (string, error) {
	// Stamped here rather than by the three callers (T-17b). `ChatEnqueuer`, the
	// scheduler and the watcher all produce this task, and a fourth producer
	// added next year would otherwise be a trace that stops at the queue with
	// nothing to say it does.
	if p.Trace == nil {
		p.Trace = tracing.Inject(ctx)
	}
	if p.EnqueuedAt.IsZero() {
		p.EnqueuedAt = time.Now()
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal chat payload: %w", err)
	}
	task := asynq.NewTask(TypeChatRun, body)
	info, err := e.client.EnqueueContext(ctx, task,
		asynq.MaxRetry(3),
		asynq.Timeout(10*time.Minute),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		return "", fmt.Errorf("enqueue chat:run: %w", err)
	}
	return info.ID, nil
}

// QueueVideo is where a video render runs (T-V3).
//
// Its own queue, not its own task type: a video is a longer instance of the
// same job, and a second type would be a second handler to keep in step. What
// it must not share is the *lane* — one video is minutes of a worker slot, and
// three of them on the default queue would stall every PDF, webhook and
// business-inference task behind them.
const QueueVideo = "video"

// EnqueueReportRender dispatches a render that overran its synchronous window
// (T-A2), or a video, which never had a synchronous window to overrun (T-V3).
//
// MaxRetry is 1, not 3: a render is deterministic, so a spec that panicked a
// renderer will panic it again, and three attempts only delay the failure the
// caller is polling for. For a video the argument is stronger — a retried
// render bills a second time for a file the tenant may already have — and
// asynq's retry is limited to what the worker chooses to return an error for,
// which is transport failures only.
func (e *Enqueuer) EnqueueReportRender(ctx context.Context, p ReportRenderPayload) (string, error) {
	if p.Trace == nil {
		p.Trace = tracing.Inject(ctx)
	}
	if p.EnqueuedAt.IsZero() {
		p.EnqueuedAt = time.Now()
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal report render payload: %w", err)
	}
	opts := []asynq.Option{
		asynq.MaxRetry(1),
		asynq.Timeout(5 * time.Minute),
		asynq.Retention(24 * time.Hour),
	}
	if p.Spec.Format == "mp4" {
		// Twenty minutes: above the render service's own ten-minute wall clock
		// and above the client's deadline, so the thing that gives up first is
		// the one with a message to write. An asynq timeout kills the handler
		// mid-call and leaves the report row `running` forever.
		opts = []asynq.Option{
			asynq.MaxRetry(1),
			asynq.Timeout(20 * time.Minute),
			asynq.Retention(24 * time.Hour),
			asynq.Queue(QueueVideo),
		}
	}
	info, err := e.client.EnqueueContext(ctx, asynq.NewTask(TypeReportRender, body), opts...)
	if err != nil {
		return "", fmt.Errorf("enqueue report:render: %w", err)
	}
	return info.ID, nil
}

// EnqueueWebhookDelivery queues one signed callback (T-A2).
//
// MaxRetry(5) is the retry budget, and asynq's default backoff between
// attempts is exponential with jitter — which is what a receiver that is down
// needs, and what a fixed interval would fail to give it. The Deliverer stops
// calling the row pending at the same count, so the log agrees with the queue.
func (e *Enqueuer) EnqueueWebhookDelivery(ctx context.Context, deliveryID string) error {
	body, err := json.Marshal(WebhookDeliverPayload{DeliveryID: deliveryID})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	_, err = e.client.EnqueueContext(ctx, asynq.NewTask(TypeWebhookDeliver, body),
		asynq.MaxRetry(5),
		asynq.Timeout(30*time.Second),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("enqueue webhook:deliver: %w", err)
	}
	return nil
}

// EnqueueBusinessInference queues one source description pass (T-B2).
//
// MaxRetry(2) and a Unique window: the triggers are deliberately loose — a
// connection being added, a successful test, a tenant pressing Re-scan — so the
// same source can be queued three times in a minute by a tenant clicking
// through onboarding. The pass itself is idempotent (an unchanged fingerprint
// spends no LLM call), and this keeps the queue from carrying the duplicates at
// all. A rejected duplicate is not an error the caller should surface: the work
// it asked for is already queued.
func (e *Enqueuer) EnqueueBusinessInference(ctx context.Context, companyID, connectionID string, force bool) error {
	body, err := json.Marshal(BusinessInferPayload{
		CompanyID: companyID, ConnectionID: connectionID, Force: force,
	})
	if err != nil {
		return fmt.Errorf("marshal business inference payload: %w", err)
	}
	_, err = e.client.EnqueueContext(ctx, asynq.NewTask(TypeBusinessInfer, body),
		asynq.MaxRetry(2),
		asynq.Timeout(3*time.Minute),
		asynq.Retention(24*time.Hour),
		asynq.Unique(2*time.Minute),
	)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enqueue business:infer: %w", err)
	}
	return nil
}

// EnqueueDocumentParse queues one uploaded PDF for reading (T-P1).
//
// Unique over an hour, which is longer than any other job in this file and is
// about cost rather than tidiness: a parse can spend money on the OCR path
// (T-P3), and the two ways this gets queued twice — a retried upload request and
// a re-parse pressed by somebody watching a slow document — are both the same
// work. MaxRetry(2) because the failures that are worth retrying here are
// transient (the sidecar restarting); a PDF this parser cannot read will not
// become readable on the third attempt.
//
// A rejected duplicate is not an error the caller should surface, for
// EnqueueBusinessInference's reason: the work it asked for is already queued.
func (e *Enqueuer) EnqueueDocumentParse(ctx context.Context, documentID string) error {
	body, err := json.Marshal(DocumentParsePayload{DocumentID: documentID})
	if err != nil {
		return fmt.Errorf("marshal document parse payload: %w", err)
	}
	_, err = e.client.EnqueueContext(ctx, asynq.NewTask(TypeDocumentParse, body),
		asynq.MaxRetry(2),
		asynq.Timeout(15*time.Minute),
		asynq.Retention(24*time.Hour),
		asynq.Unique(time.Hour),
	)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enqueue document:parse: %w", err)
	}
	return nil
}

// ParseChatRun unmarshals the asynq task payload back into ChatRunPayload.
// Lives here so worker handlers don't have to touch JSON directly.
func ParseChatRun(data []byte) (ChatRunPayload, error) {
	var p ChatRunPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse chat payload: %w", err)
	}
	return p, nil
}

// ParseScheduledRun unmarshals a scheduled:run task payload.
func ParseScheduledRun(data []byte) (ScheduledRunPayload, error) {
	var p ScheduledRunPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse scheduled payload: %w", err)
	}
	return p, nil
}
