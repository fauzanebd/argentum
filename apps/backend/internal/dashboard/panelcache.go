package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// PanelCache is T-D8: a short-TTL Redis cache in front of the tenant warehouse,
// with per-process request collapsing in front of that.
//
// **Keyed on content, not on (dashboard_id, panel_id).** A definition edit and
// a filter change both produce a different key and therefore invalidate for
// free. There is no invalidation hook to write and so no edit path that can
// forget to call one — which is the failure mode a keyed-by-id cache would have
// shipped with, discovered later by a tenant reading yesterday's number.
//
// **`connVersion` is in the key and it is the component that is expensive to
// omit.** A rotated DSN can point at an entirely different database; a cache
// keyed on SQL alone would go on serving the old warehouse's figures under the
// new connection, and it would look like staleness rather than like answering
// from a database the tenant disconnected.
//
// **No stale-while-revalidate, and no per-process L1.** The failure that is real
// on day one is the thundering herd — twenty people open the same dashboard at
// 09:00 and twelve panels each run twenty times against a customer's warehouse.
// Collapsing in-flight duplicates fixes that. An in-process copy of the *value*
// would be a different thing and a worse one: two API replicas could show two
// figures for the same URL, and a dashboard's whole value is that the number is
// current. Redis is the only value layer.
type PanelCache struct {
	rdb *redis.Client
	ttl time.Duration

	mu    sync.Mutex
	calls map[string]*inflight
}

// inflight is one execution other callers are waiting on. About forty lines of
// singleflight, hand-rolled rather than taking golang.org/x/sync: the module
// does not depend on it and this is its only consumer.
type inflight struct {
	wg   sync.WaitGroup
	val  []byte
	err  error
	subs int
}

// NewPanelCache returns a cache, or nil when this deployment has no Redis. A
// nil *PanelCache is usable and simply never caches, so the resolver has no
// branch and a deployment without Redis behaves exactly as it did before T-D8.
func NewPanelCache(rdb *redis.Client, ttl time.Duration) *PanelCache {
	if rdb == nil || ttl <= 0 {
		return nil
	}
	return &PanelCache{rdb: rdb, ttl: ttl, calls: make(map[string]*inflight)}
}

// Outcome says how a panel was answered, and the query log (T-D9) writes a row
// only for OutcomeMiss.
//
// Collapsed is not a miss. Two hundred browsers waiting on one execution read
// the customer's warehouse once, and recording two hundred rows for it would
// make the log answer "how many people looked" when the question it exists to
// answer is "what ran against my database".
type Outcome int

const (
	OutcomeMiss Outcome = iota
	OutcomeHit
	OutcomeCollapsed
)

func (o Outcome) String() string {
	switch o {
	case OutcomeHit:
		return "hit"
	case OutcomeCollapsed:
		return "collapsed"
	default:
		return "miss"
	}
}

// Do returns the cached bytes for key, or runs fn once and caches what it
// returns. Concurrent callers for the same key share one execution.
//
// A cache that cannot be read is not an error: Redis being down should slow a
// dashboard, not break it, so a failed GET falls through to fn and a failed SET
// is logged and ignored.
func (c *PanelCache) Do(ctx context.Context, key string, fn func() ([]byte, error)) ([]byte, Outcome, error) {
	if c == nil {
		b, err := fn()
		return b, OutcomeMiss, err
	}

	// The value layer and the collapsing layer are independent. A cache with no
	// Redis still collapses duplicate in-flight executions, which is the half
	// that needs no shared store and fixes the failure that is real on day one.
	if c.rdb != nil {
		if b, err := c.rdb.Get(ctx, key).Bytes(); err == nil {
			return b, OutcomeHit, nil
		} else if !errors.Is(err, redis.Nil) {
			logrus.WithError(err).WithField("key", key).Debug("panel cache read failed; falling through to the warehouse")
		}
	}

	c.mu.Lock()
	if call, ok := c.calls[key]; ok {
		call.subs++
		c.mu.Unlock()
		call.wg.Wait()
		return call.val, OutcomeCollapsed, call.err
	}
	call := &inflight{}
	call.wg.Add(1)
	c.calls[key] = call
	c.mu.Unlock()

	call.val, call.err = fn()

	c.mu.Lock()
	delete(c.calls, key)
	collapsed := call.subs
	c.mu.Unlock()
	call.wg.Done()

	if call.err == nil {
		if err := c.setIfConfigured(ctx, key, call.val); err != nil {
			logrus.WithError(err).WithField("key", key).Debug("panel cache write failed; the answer is still correct")
		}
		if collapsed > 0 {
			logrus.WithFields(logrus.Fields{"key": key, "collapsed": collapsed}).
				Debug("panel request collapsing saved warehouse executions")
		}
	}
	return call.val, OutcomeMiss, call.err
}

// setIfConfigured writes through to Redis when there is one.
func (c *PanelCache) setIfConfigured(ctx context.Context, key string, val []byte) error {
	if c.rdb == nil {
		return nil
	}
	return c.rdb.Set(ctx, key, val, c.ttl).Err()
}

const panelKeyPrefix = "dash:panel:1:"

// unit separator, so no component can forge a boundary by containing the
// delimiter — an SQL literal is allowed to contain anything.
const keySep = "\x1f"

func hashKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, keySep)))
	return panelKeyPrefix + hex.EncodeToString(sum[:])
}

// SQLPanelKey is the key for a panel that runs a statement against a tenant
// warehouse. Every input that can change the answer is in it.
func SQLPanelKey(companyID, sourceID, connVersion, dbType, renderedSQL, argsJSON string, maxRows int) string {
	return hashKey("sql", companyID, sourceID, connVersion, dbType, renderedSQL, argsJSON, strconv.Itoa(maxRows))
}

// MetricPanelKey is the key for a metric panel, which keys on the metric and
// the window rather than on rendered SQL because MetricService owns its own
// rendering and this package never sees the statement.
func MetricPanelKey(companyID, metricKey string, from, to time.Time) string {
	return hashKey("metric", companyID, metricKey,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
}
