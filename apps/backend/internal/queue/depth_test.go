package queue

import (
	"os"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/fauzanebd/argentum/internal/metrics"
)

// The poller's whole job is to report a number this process did not produce,
// so the only test worth writing needs a real Redis. It is skipped unless
// ARGENTUM_TEST_REDIS names one — CI has no Redis, and a test that silently
// passes without its dependency is worse than no test.
//
//	ARGENTUM_TEST_REDIS=localhost:6380 go test ./internal/queue/ -run Depth
func TestDepthPollerReportsWhatRedisHolds(t *testing.T) {
	addr := os.Getenv("ARGENTUM_TEST_REDIS")
	if addr == "" {
		t.Skip("set ARGENTUM_TEST_REDIS to run this against a live Redis")
	}
	opt := asynq.RedisClientOpt{Addr: addr}

	// A queue of this test's own, so a developer's local backlog cannot make
	// the assertion pass and this test cannot disturb it.
	const queueName = "depthtest"
	client := asynq.NewClient(opt)
	defer client.Close()

	sink := metrics.NewCollector()
	poller := NewDepthPoller(opt, sink, time.Second)

	inspector := asynq.NewInspector(opt)
	defer inspector.Close()
	t.Cleanup(func() {
		// Leave nothing behind: the queue itself, not just its tasks, or the
		// next run of this test starts against a queue that already exists.
		_, _ = inspector.DeleteAllPendingTasks(queueName)
		_ = inspector.DeleteQueue(queueName, true)
	})

	if _, err := client.Enqueue(
		asynq.NewTask("depth:probe", nil),
		asynq.Queue(queueName),
		asynq.Retention(time.Minute),
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	poller.sample()
	got, ok := sink.GetSnapshot().Queues[queueName]
	if !ok {
		t.Fatalf("queue %q is missing from the sample: %+v", queueName, sink.GetSnapshot().Queues)
	}
	if got.Pending != 1 {
		t.Errorf("pending = %d, want 1 — the gauge is not reading Redis", got.Pending)
	}

	// And it must fall again: a depth that only ever rises is a counter with a
	// misleading name.
	if _, err := inspector.DeleteAllPendingTasks(queueName); err != nil {
		t.Fatalf("drain: %v", err)
	}
	poller.sample()
	if after := sink.GetSnapshot().Queues[queueName]; after.Pending != 0 {
		t.Errorf("pending after drain = %d, want 0", after.Pending)
	}
}
