package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
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

// EnqueueReportRender dispatches a render that overran its synchronous window
// (T-A2). MaxRetry is 1, not 3: a render is deterministic, so a spec that
// panicked a renderer will panic it again, and three attempts only delay the
// failure the caller is polling for.
func (e *Enqueuer) EnqueueReportRender(ctx context.Context, p ReportRenderPayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal report render payload: %w", err)
	}
	info, err := e.client.EnqueueContext(ctx, asynq.NewTask(TypeReportRender, body),
		asynq.MaxRetry(1),
		asynq.Timeout(5*time.Minute),
		asynq.Retention(24*time.Hour),
	)
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
