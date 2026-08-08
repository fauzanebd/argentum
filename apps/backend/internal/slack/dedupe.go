package slack

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// DedupeTTL bounds how long an event id is remembered. Slack gives up after
// three retries spread over roughly half a minute, so ten minutes is far more
// than enough and still small enough that the keys cost nothing.
const DedupeTTL = 10 * time.Minute

// Deduper answers "have I already handled this event?".
//
// It exists because Slack redelivers aggressively: any ack slower than three
// seconds is retried, and an agent turn is always slower than three seconds if
// the enqueue path stalls. Two deliveries of one event is two agent runs, two
// charges against the tenant's credit, and two answers posted into the same
// thread.
//
// The retry header alone is not enough. It catches the ordinary case, but
// Slack also redelivers without it in failover, and a handler that trusts a
// header for a billing-relevant decision is trusting the caller.
type Deduper interface {
	// FirstSight reports whether this is the first delivery of eventID.
	// Errors are the caller's to interpret — the safe reading is "process it",
	// because dropping a real question is worse than a rare duplicate.
	FirstSight(ctx context.Context, appID, eventID string) (bool, error)
}

// RedisDeduper claims event ids with SET NX, which is atomic across API
// replicas — the property that matters, since Slack's retry may well land on
// a different pod than the delivery it is retrying.
type RedisDeduper struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewRedisDeduper returns nil when rdb is nil, so a deployment without Redis
// degrades to the retry-header check rather than refusing to start.
func NewRedisDeduper(rdb *redis.Client) *RedisDeduper {
	if rdb == nil {
		return nil
	}
	return &RedisDeduper{rdb: rdb, ttl: DedupeTTL}
}

// DedupeKey is the Redis key for one event. Namespaced by app id so two
// tenants cannot collide on an id Slack only promises to keep unique per app.
func DedupeKey(appID, eventID string) string {
	return "slack:evt:" + appID + ":" + eventID
}

func (d *RedisDeduper) FirstSight(ctx context.Context, appID, eventID string) (bool, error) {
	if d == nil || d.rdb == nil || eventID == "" {
		return true, nil
	}
	// SetArgs with Mode "NX" rather than SetNX, which go-redis deprecated —
	// the same substitution internal/idempotency made, and for the same
	// reason. A key that already exists comes back as redis.Nil rather than as
	// an error: that is the duplicate, not a failure.
	err := d.rdb.SetArgs(ctx, DedupeKey(appID, eventID), "1",
		redis.SetArgs{Mode: "NX", TTL: d.ttl}).Err()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

var _ Deduper = (*RedisDeduper)(nil)
