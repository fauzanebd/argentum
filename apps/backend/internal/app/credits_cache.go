package app

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// RedisBudgetCache is the production BudgetCache. Every method swallows its
// error and degrades to a miss: a Redis outage must cost an extra query per
// turn, never a refused turn or a failed one.
type RedisBudgetCache struct{ rdb *redis.Client }

// NewRedisBudgetCache returns nil when rdb is nil, so a caller can hand the
// result straight to WithCredits without a branch. Both methods guard their
// receiver because a nil *RedisBudgetCache stored in a BudgetCache interface
// is not a nil interface — the branch the caller skipped has to happen here
// instead, and a missing guard would panic on the first turn after a
// deployment without Redis.
func NewRedisBudgetCache(rdb *redis.Client) *RedisBudgetCache {
	if rdb == nil {
		return nil
	}
	return &RedisBudgetCache{rdb: rdb}
}

func (c *RedisBudgetCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	return raw, true
}

func (c *RedisBudgetCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) {
	if c == nil {
		return
	}
	if err := c.rdb.Set(ctx, key, val, ttl).Err(); err != nil {
		logrus.WithError(err).Debug("budget cache write failed; next check will re-query")
	}
}
