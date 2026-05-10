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
