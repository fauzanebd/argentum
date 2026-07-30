// Package metrics holds the counters and histograms the API serves at
// /metrics: query and LLM totals, streaming-metering health, and — since T-A5 —
// per-route and per-key `/v1` request profiles.
//
// The wire format is JSON, not Prometheus exposition, whatever this comment
// used to claim. Converting it is T-17's job; the histogram added by T-A5 is
// already shaped for that conversion (cumulative buckets keyed by upper bound,
// with a `+Inf` overflow), so the change is a serializer rather than a
// remodelling.
package metrics

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Collector gathers and stores metrics
type Collector struct {
	mu sync.RWMutex

	// Query metrics
	queriesTotal       int64
	queriesCached      int64
	queriesFailed      int64
	queryDurationTotal int64 // nanoseconds

	// LLM metrics
	llmRequestsTotal  int64
	llmTokensInTotal  int64
	llmTokensOutTotal int64
	llmCostTotalMicro uint64 // Store as micro-dollars for atomic ops

	// Streaming-turn metering health (T-02c). A turn that reports no usage at
	// all is an unbilled turn — finding C-2 was exactly that, invisible for
	// nine weeks. Counting it makes a regression a number rather than a
	// discovery during a smoke test.
	llmStreamTurnsTotal       int64
	llmStreamTurnsNoUsage     int64
	llmStreamUsageEventsTotal int64

	// Cache metrics
	cacheHits   int64
	cacheMisses int64

	// Job metrics
	jobsCreated   int64
	jobsCompleted int64
	jobsFailed    int64

	// Conversation metrics
	conversationsActive int64
	contextResets       int64

	// Public API request metrics (T-A5), guarded by mu rather than by atomics:
	// the unit of update is a map entry and a bucket array, not a counter.
	apiRoutes        map[string]*routeAgg
	apiKeys          map[string]*keyAgg
	apiKeysUntracked int64

	// Timestamp of last reset
	lastReset time.Time
}

// apiLatencyBucketsMS are the histogram's upper bounds, in milliseconds. They
// are Prometheus's defaults shifted for an HTTP API that talks to Postgres and
// an LLM: the bottom of the range separates "answered from memory" from "did a
// query", and the top has to reach a synchronous chat turn, which T-A3 caps at
// 120 seconds.
var apiLatencyBucketsMS = []int64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 120000}

// maxTrackedAPIKeys bounds the per-key cardinality. Routes are bounded by the
// router, but key ids are minted by tenants, and an unbounded label set on a
// scraped endpoint is how a metrics exporter becomes the process's largest
// allocation. Overflow is counted so the truncation is visible rather than
// silent.
const maxTrackedAPIKeys = 500

// routeAgg is one route's counters. byStatus is exact statuses rather than
// classes: the whole point of the endpoint is telling a 403 from a 429.
type routeAgg struct {
	requests int64
	errors   int64
	byStatus map[int]int64
	latSumMS int64
	latMaxMS int64
	buckets  []int64 // len(apiLatencyBucketsMS)+1; last is the +Inf overflow
}

type keyAgg struct {
	requests int64
	errors   int64
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		apiRoutes: map[string]*routeAgg{},
		apiKeys:   map[string]*keyAgg{},
		lastReset: time.Now(),
	}
}

// defaultCollector is the process-wide collector used by code too deep in the
// call graph to be handed one — currently app.MeteredLLM. cmd/api serves it at
// /metrics; the worker increments its own copy, which stays process-local
// until T-17 gives the worker an exporter.
var defaultCollector = NewCollector()

// Default returns the process-wide collector. Never nil.
func Default() *Collector { return defaultCollector }

// RecordQuery records a query execution
func (c *Collector) RecordQuery(duration time.Duration, cached, failed bool) {
	atomic.AddInt64(&c.queriesTotal, 1)
	atomic.AddInt64(&c.queryDurationTotal, duration.Nanoseconds())

	if cached {
		atomic.AddInt64(&c.queriesCached, 1)
	}
	if failed {
		atomic.AddInt64(&c.queriesFailed, 1)
	}
}

// RecordLLMRequest records an LLM API call
func (c *Collector) RecordLLMRequest(tokensIn, tokensOut int, cost float64) {
	atomic.AddInt64(&c.llmRequestsTotal, 1)
	atomic.AddInt64(&c.llmTokensInTotal, int64(tokensIn))
	atomic.AddInt64(&c.llmTokensOutTotal, int64(tokensOut))

	// Convert cost to micro-dollars and add atomically
	costMicro := uint64(cost * 1000000)
	atomic.AddUint64(&c.llmCostTotalMicro, costMicro)
}

// RecordLLMStreamTurn records the completion of one streaming LLM turn and how
// many provider usage reports it produced. usageEvents == 0 means the turn was
// unbilled: that is the C-2 failure mode, and it is what
// llm.stream_turns_without_usage counts.
func (c *Collector) RecordLLMStreamTurn(usageEvents int) {
	atomic.AddInt64(&c.llmStreamTurnsTotal, 1)
	if usageEvents <= 0 {
		atomic.AddInt64(&c.llmStreamTurnsNoUsage, 1)
		return
	}
	atomic.AddInt64(&c.llmStreamUsageEventsTotal, int64(usageEvents))
}

// RecordAPIRequest records one finished `/v1` request (T-A5).
//
// Called from internal/apiobs for every request, including the ones no tenant
// can be shown — an unauthenticated 401 has no key id, and counting it here is
// the only place it is counted at all.
//
// route is the gin pattern; an empty one (no route matched) is folded into
// "unmatched" rather than recorded under the concrete path, because a 404
// sweep would otherwise mint a label per URL somebody guessed.
func (c *Collector) RecordAPIRequest(method, route, keyID string, status int, d time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	label := method + " " + route
	ms := d.Milliseconds()
	failed := status < 200 || status >= 300

	c.mu.Lock()
	defer c.mu.Unlock()

	r := c.apiRoutes[label]
	if r == nil {
		r = &routeAgg{
			byStatus: map[int]int64{},
			buckets:  make([]int64, len(apiLatencyBucketsMS)+1),
		}
		c.apiRoutes[label] = r
	}
	r.requests++
	if failed {
		r.errors++
	}
	r.byStatus[status]++
	r.latSumMS += ms
	if ms > r.latMaxMS {
		r.latMaxMS = ms
	}
	r.buckets[bucketIndex(ms)]++

	if keyID == "" {
		return
	}
	k := c.apiKeys[keyID]
	if k == nil {
		if len(c.apiKeys) >= maxTrackedAPIKeys {
			c.apiKeysUntracked++
			return
		}
		k = &keyAgg{}
		c.apiKeys[keyID] = k
	}
	k.requests++
	if failed {
		k.errors++
	}
}

// bucketIndex finds the first bound ms falls under, or the overflow slot.
func bucketIndex(ms int64) int {
	for i, bound := range apiLatencyBucketsMS {
		if ms <= bound {
			return i
		}
	}
	return len(apiLatencyBucketsMS)
}

// RecordCacheHit records a cache hit
func (c *Collector) RecordCacheHit() {
	atomic.AddInt64(&c.cacheHits, 1)
}

// RecordCacheMiss records a cache miss
func (c *Collector) RecordCacheMiss() {
	atomic.AddInt64(&c.cacheMisses, 1)
}

// RecordJobCreated records job creation
func (c *Collector) RecordJobCreated() {
	atomic.AddInt64(&c.jobsCreated, 1)
}

// RecordJobCompleted records job completion
func (c *Collector) RecordJobCompleted() {
	atomic.AddInt64(&c.jobsCompleted, 1)
}

// RecordJobFailed records job failure
func (c *Collector) RecordJobFailed() {
	atomic.AddInt64(&c.jobsFailed, 1)
}

// RecordConversationStarted records new conversation
func (c *Collector) RecordConversationStarted() {
	atomic.AddInt64(&c.conversationsActive, 1)
}

// RecordConversationEnded records conversation end
func (c *Collector) RecordConversationEnded() {
	atomic.AddInt64(&c.conversationsActive, -1)
}

// RecordContextReset records context reset
func (c *Collector) RecordContextReset() {
	atomic.AddInt64(&c.contextResets, 1)
}

// GetSnapshot returns current metrics snapshot
func (c *Collector) GetSnapshot() MetricsSnapshot {
	queriesTotal := atomic.LoadInt64(&c.queriesTotal)
	queriesCached := atomic.LoadInt64(&c.queriesCached)
	cacheHits := atomic.LoadInt64(&c.cacheHits)
	cacheMisses := atomic.LoadInt64(&c.cacheMisses)

	var cacheHitRate float64
	totalCacheOps := cacheHits + cacheMisses
	if totalCacheOps > 0 {
		cacheHitRate = float64(cacheHits) / float64(totalCacheOps) * 100
	}

	var avgQueryDuration time.Duration
	if queriesTotal > 0 {
		avgQueryDuration = time.Duration(atomic.LoadInt64(&c.queryDurationTotal) / queriesTotal)
	}

	return MetricsSnapshot{
		Timestamp: time.Now(),

		Queries: QueryMetrics{
			Total:         queriesTotal,
			Cached:        queriesCached,
			Failed:        atomic.LoadInt64(&c.queriesFailed),
			AvgDurationMs: avgQueryDuration.Milliseconds(),
			CacheHitRate:  cacheHitRate,
		},

		LLM: LLMMetrics{
			RequestsTotal:           atomic.LoadInt64(&c.llmRequestsTotal),
			TokensInTotal:           atomic.LoadInt64(&c.llmTokensInTotal),
			TokensOutTotal:          atomic.LoadInt64(&c.llmTokensOutTotal),
			CostTotal:               float64(atomic.LoadUint64(&c.llmCostTotalMicro)) / 1000000,
			StreamTurnsTotal:        atomic.LoadInt64(&c.llmStreamTurnsTotal),
			StreamTurnsWithoutUsage: atomic.LoadInt64(&c.llmStreamTurnsNoUsage),
			StreamUsageEventsTotal:  atomic.LoadInt64(&c.llmStreamUsageEventsTotal),
		},

		Cache: CacheMetrics{
			Hits:    cacheHits,
			Misses:  cacheMisses,
			HitRate: cacheHitRate,
		},

		Jobs: JobMetrics{
			Created:   atomic.LoadInt64(&c.jobsCreated),
			Completed: atomic.LoadInt64(&c.jobsCompleted),
			Failed:    atomic.LoadInt64(&c.jobsFailed),
		},

		Conversations: ConversationMetrics{
			Active:        atomic.LoadInt64(&c.conversationsActive),
			ContextResets: atomic.LoadInt64(&c.contextResets),
		},

		APIV1: c.apiSnapshot(),

		UptimeSeconds: time.Since(c.lastReset).Seconds(),
	}
}

// apiSnapshot copies the per-route and per-key maps out from under the lock.
// Copies, not references: a scrape must not hand the caller a map that request
// goroutines are still writing to.
func (c *Collector) apiSnapshot() APIV1Metrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := APIV1Metrics{
		Routes:        make(map[string]RouteMetrics, len(c.apiRoutes)),
		Keys:          make(map[string]KeyMetrics, len(c.apiKeys)),
		KeysUntracked: c.apiKeysUntracked,
	}
	for label, r := range c.apiRoutes {
		byStatus := make(map[string]int64, len(r.byStatus))
		for status, n := range r.byStatus {
			byStatus[strconv.Itoa(status)] = n
		}
		// Cumulative, Prometheus-style: bucket `le=100` counts everything at or
		// under 100ms, not just what landed between 50 and 100. A consumer
		// computing a quantile needs the cumulative form, and converting later
		// means every consumer converts.
		cumulative := make(map[string]int64, len(r.buckets))
		var running int64
		for i, n := range r.buckets {
			running += n
			bound := "+Inf"
			if i < len(apiLatencyBucketsMS) {
				bound = strconv.FormatInt(apiLatencyBucketsMS[i], 10)
			}
			cumulative[bound] = running
		}
		out.Routes[label] = RouteMetrics{
			Requests: r.requests,
			Errors:   r.errors,
			ByStatus: byStatus,
			Latency: LatencyHistogram{
				Buckets: cumulative,
				SumMS:   r.latSumMS,
				Count:   r.requests,
				MaxMS:   r.latMaxMS,
			},
		}
	}
	for id, k := range c.apiKeys {
		out.Keys[id] = KeyMetrics{Requests: k.requests, Errors: k.errors}
	}
	return out
}

// WithoutKeyLabels returns the snapshot with the per-key block removed.
//
// `/metrics` has no credential of its own (T-17 owns fixing that), and a key id
// is a tenant's identifier for a credential they hold. Route-level numbers say
// nothing about who called; a key id does, so the serving side strips them
// unless the endpoint is actually protected. See cmd/api/health.go.
func (s MetricsSnapshot) WithoutKeyLabels() MetricsSnapshot {
	s.APIV1.Keys = nil
	s.APIV1.KeysUntracked = 0
	return s
}

// Reset resets all metrics
func (c *Collector) Reset() {
	atomic.StoreInt64(&c.queriesTotal, 0)
	atomic.StoreInt64(&c.queriesCached, 0)
	atomic.StoreInt64(&c.queriesFailed, 0)
	atomic.StoreInt64(&c.queryDurationTotal, 0)
	atomic.StoreInt64(&c.llmRequestsTotal, 0)
	atomic.StoreInt64(&c.llmTokensInTotal, 0)
	atomic.StoreInt64(&c.llmTokensOutTotal, 0)
	atomic.StoreUint64(&c.llmCostTotalMicro, 0)
	atomic.StoreInt64(&c.llmStreamTurnsTotal, 0)
	atomic.StoreInt64(&c.llmStreamTurnsNoUsage, 0)
	atomic.StoreInt64(&c.llmStreamUsageEventsTotal, 0)
	atomic.StoreInt64(&c.cacheHits, 0)
	atomic.StoreInt64(&c.cacheMisses, 0)
	atomic.StoreInt64(&c.jobsCreated, 0)
	atomic.StoreInt64(&c.jobsCompleted, 0)
	atomic.StoreInt64(&c.jobsFailed, 0)
	atomic.StoreInt64(&c.contextResets, 0)

	c.mu.Lock()
	c.apiRoutes = map[string]*routeAgg{}
	c.apiKeys = map[string]*keyAgg{}
	c.apiKeysUntracked = 0
	c.lastReset = time.Now()
	c.mu.Unlock()
}

// MetricsSnapshot represents a point-in-time metrics snapshot
type MetricsSnapshot struct {
	Timestamp     time.Time           `json:"timestamp"`
	Queries       QueryMetrics        `json:"queries"`
	LLM           LLMMetrics          `json:"llm"`
	Cache         CacheMetrics        `json:"cache"`
	Jobs          JobMetrics          `json:"jobs"`
	Conversations ConversationMetrics `json:"conversations"`
	APIV1         APIV1Metrics        `json:"api_v1"`
	UptimeSeconds float64             `json:"uptime_seconds"`
}

// APIV1Metrics is the public API's request profile (T-A5). Two label sets, and
// they answer different questions: a route tells you what is slow or failing,
// a key tells you whose integration is doing it.
type APIV1Metrics struct {
	// Routes is keyed by "METHOD /gin/path".
	Routes map[string]RouteMetrics `json:"routes,omitempty"`
	// Keys is keyed by API key id, and is omitted entirely when `/metrics` is
	// served without a credential — see MetricsSnapshot.WithoutKeyLabels.
	Keys map[string]KeyMetrics `json:"keys,omitempty"`
	// KeysUntracked counts requests by keys beyond maxTrackedAPIKeys. Non-zero
	// means the per-key block is incomplete, which is worth knowing before
	// reading it as a total.
	KeysUntracked int64 `json:"keys_untracked,omitempty"`
}

// RouteMetrics is one route's traffic.
type RouteMetrics struct {
	Requests int64 `json:"requests"`
	// Errors counts everything outside 2xx, including the 4xx a caller caused.
	// An error rate that excluded client errors would hide the one failure mode
	// this endpoint exists to surface.
	Errors   int64            `json:"errors"`
	ByStatus map[string]int64 `json:"by_status"`
	Latency  LatencyHistogram `json:"latency_ms"`
}

// LatencyHistogram is cumulative, keyed by upper bound in milliseconds with
// "+Inf" for the overflow — the shape a quantile can be read out of.
type LatencyHistogram struct {
	Buckets map[string]int64 `json:"buckets"`
	SumMS   int64            `json:"sum_ms"`
	Count   int64            `json:"count"`
	MaxMS   int64            `json:"max_ms"`
}

// KeyMetrics is one API key's traffic, process-local. The tenant's own durable
// view of the same thing is api_request_stats — this is for whoever operates
// the deployment.
type KeyMetrics struct {
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
}

// QueryMetrics contains query-related metrics
type QueryMetrics struct {
	Total         int64   `json:"total"`
	Cached        int64   `json:"cached"`
	Failed        int64   `json:"failed"`
	AvgDurationMs int64   `json:"avg_duration_ms"`
	CacheHitRate  float64 `json:"cache_hit_rate"`
}

// LLMMetrics contains LLM usage metrics
type LLMMetrics struct {
	RequestsTotal  int64   `json:"requests_total"`
	TokensInTotal  int64   `json:"tokens_in_total"`
	TokensOutTotal int64   `json:"tokens_out_total"`
	CostTotal      float64 `json:"cost_total_usd"`
	// StreamTurnsWithoutUsage > 0 means turns are being served unbilled —
	// alert on it. See Collector.RecordLLMStreamTurn.
	StreamTurnsTotal        int64 `json:"stream_turns_total"`
	StreamTurnsWithoutUsage int64 `json:"stream_turns_without_usage"`
	StreamUsageEventsTotal  int64 `json:"stream_usage_events_total"`
}

// CacheMetrics contains cache performance metrics
type CacheMetrics struct {
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	HitRate float64 `json:"hit_rate_percent"`
}

// JobMetrics contains job processing metrics
type JobMetrics struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
}

// ConversationMetrics contains conversation metrics
type ConversationMetrics struct {
	Active        int64 `json:"active"`
	ContextResets int64 `json:"context_resets"`
}
