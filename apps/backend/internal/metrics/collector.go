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
	"maps"
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

	// Domain counters (T-17). Bounded label sets, all of them: a tool name comes
	// from the registry, an action kind from the action registry, a channel from
	// the Channel enum. Nothing here is labelled by anything a tenant types, for
	// the reason maxTrackedAPIKeys exists — an unbounded label set on a scraped
	// endpoint is how an exporter becomes the process's largest allocation.
	watcherFires     map[string]int64 // "breached" | "quiet" | "suppressed"
	actionExecutions map[string]int64 // action kind -> count
	actionFailures   map[string]int64
	toolCalls        map[string]*toolAgg // tool name -> count/errors/duration
	turnDurations    *durationAgg
	llmLatency       map[string]*durationAgg // model -> latency

	// Public API request metrics (T-A5), guarded by mu rather than by atomics:
	// the unit of update is a map entry and a bucket array, not a counter.
	apiRoutes        map[string]*routeAgg
	apiKeys          map[string]*keyAgg
	apiKeysUntracked int64

	// Queue depth (T-17), sampled rather than counted: every other number here
	// is a total this process accumulated, while a depth is whatever Redis says
	// right now. Written by the poller in internal/queue, read by the exposition
	// — nil until something samples, which is how a process with no poller
	// exports no queue series rather than exporting zeros it cannot vouch for.
	queueDepths map[string]QueueDepth

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

// toolAgg is one tool's traffic across this process's lifetime.
type toolAgg struct {
	calls  int64
	errors int64
	sumMS  int64
	maxMS  int64
}

// durationAgg is a plain sum/count/max, which is what a "how long did that
// take?" question needs before anybody has decided which quantile matters. The
// `/v1` route histogram earns its buckets because a latency SLO is stated
// against it; a turn's duration is read as a mean and a worst case.
type durationAgg struct {
	count int64
	sumMS int64
	maxMS int64
}

func (d *durationAgg) observe(ms int64) {
	d.count++
	d.sumMS += ms
	if ms > d.maxMS {
		d.maxMS = ms
	}
}

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
		apiRoutes:        map[string]*routeAgg{},
		apiKeys:          map[string]*keyAgg{},
		watcherFires:     map[string]int64{},
		actionExecutions: map[string]int64{},
		actionFailures:   map[string]int64{},
		toolCalls:        map[string]*toolAgg{},
		llmLatency:       map[string]*durationAgg{},
		turnDurations:    &durationAgg{},
		lastReset:        time.Now(),
	}
}

// defaultCollector is the process-wide collector used by code too deep in the
// call graph to be handed one — currently app.MeteredLLM. cmd/api serves it at
// /metrics; the worker increments its own copy, which stays process-local
// until T-17 gives the worker an exporter.
var defaultCollector = NewCollector()

// Default returns the process-wide collector. Never nil.
func Default() *Collector { return defaultCollector }

// SetQueueDepths replaces the sampled queue depths wholesale. Wholesale
// because a queue that has disappeared from the sample should disappear from
// the exposition too — a stale gauge reading 400 pending is worse than no
// gauge, since an operator cannot tell it from a real backlog.
func (c *Collector) SetQueueDepths(depths map[string]QueueDepth) {
	next := make(map[string]QueueDepth, len(depths))
	maps.Copy(next, depths)
	c.mu.Lock()
	c.queueDepths = next
	c.mu.Unlock()
}

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

// RecordWatcherFire counts one watcher evaluation by outcome (T-17):
// "breached", "suppressed" (a real breach inside its cooldown) or "quiet".
//
// Three labels rather than a boolean, because the operational question is
// exactly the one the watcher events sheet had to be fixed to answer: a watcher
// that is firing constantly and a watcher that is suppressing constantly are
// different problems.
func (c *Collector) RecordWatcherFire(outcome string) {
	if c == nil || outcome == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watcherFires == nil {
		c.watcherFires = map[string]int64{}
	}
	c.watcherFires[outcome]++
}

// RecordActionExecution counts one action that ran, by kind and outcome. The
// kinds come from the action registry, so the label set is bounded by code.
func (c *Collector) RecordActionExecution(kind string, ok bool) {
	if c == nil || kind == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.actionExecutions == nil {
		c.actionExecutions = map[string]int64{}
		c.actionFailures = map[string]int64{}
	}
	c.actionExecutions[kind]++
	if !ok {
		c.actionFailures[kind]++
	}
}

// RecordToolCall counts one tool call and how long it took. Called from the
// audit decorator, which is the one place every tool call passes through — the
// same reason the audit row is written there.
func (c *Collector) RecordToolCall(tool string, d time.Duration, failed bool) {
	if c == nil || tool == "" {
		return
	}
	ms := d.Milliseconds()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.toolCalls == nil {
		c.toolCalls = map[string]*toolAgg{}
	}
	agg := c.toolCalls[tool]
	if agg == nil {
		agg = &toolAgg{}
		c.toolCalls[tool] = agg
	}
	agg.calls++
	agg.sumMS += ms
	if ms > agg.maxMS {
		agg.maxMS = ms
	}
	if failed {
		agg.errors++
	}
}

// RecordTurn counts one completed agent turn and its wall clock.
func (c *Collector) RecordTurn(d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turnDurations == nil {
		c.turnDurations = &durationAgg{}
	}
	c.turnDurations.observe(d.Milliseconds())
}

// RecordLLMLatency records one model call's wall clock, by model. The label is
// the model id, which is deployment config rather than tenant input — a tenant
// with their own key names a model from the same provider vocabulary.
func (c *Collector) RecordLLMLatency(model string, d time.Duration) {
	if c == nil || model == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.llmLatency == nil {
		c.llmLatency = map[string]*durationAgg{}
	}
	agg := c.llmLatency[model]
	if agg == nil {
		agg = &durationAgg{}
		c.llmLatency[model] = agg
	}
	agg.observe(d.Milliseconds())
}

// RecordCacheHit records a cache hit.
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

		APIV1:  c.apiSnapshot(),
		Domain: c.domainSnapshot(),
		Queues: c.queueSnapshot(),

		UptimeSeconds: time.Since(c.lastReset).Seconds(),
	}
}

// queueSnapshot copies the sampled depths out from under the lock. Nil when
// nothing has sampled, which the exposition reads as "export no queue series".
func (c *Collector) queueSnapshot() map[string]QueueDepth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.queueDepths) == 0 {
		return nil
	}
	out := make(map[string]QueueDepth, len(c.queueDepths))
	maps.Copy(out, c.queueDepths)
	return out
}

// domainSnapshot copies the T-17 counters out from under the lock, for the same
// reason apiSnapshot does: a scrape must not be handed a map that request
// goroutines are still writing to.
func (c *Collector) domainSnapshot() DomainMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := DomainMetrics{
		WatcherFires:     copyCounts(c.watcherFires),
		ActionExecutions: copyCounts(c.actionExecutions),
		ActionFailures:   copyCounts(c.actionFailures),
		Tools:            make(map[string]ToolMetrics, len(c.toolCalls)),
		LLMLatency:       make(map[string]DurationMetrics, len(c.llmLatency)),
	}
	for name, agg := range c.toolCalls {
		out.Tools[name] = ToolMetrics{
			Calls: agg.calls, Errors: agg.errors, SumMS: agg.sumMS, MaxMS: agg.maxMS,
		}
	}
	for model, agg := range c.llmLatency {
		out.LLMLatency[model] = DurationMetrics{Count: agg.count, SumMS: agg.sumMS, MaxMS: agg.maxMS}
	}
	if c.turnDurations != nil {
		out.Turns = DurationMetrics{
			Count: c.turnDurations.count, SumMS: c.turnDurations.sumMS, MaxMS: c.turnDurations.maxMS,
		}
	}
	return out
}

func copyCounts(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
	// Domain is what the product did rather than what the process did (T-17):
	// turns, tool calls, watcher fires, action executions, LLM latency by model.
	Domain DomainMetrics `json:"domain"`
	// Queues is sampled from Redis rather than accumulated here, and is absent
	// on a process with no poller — see Collector.SetQueueDepths.
	Queues        map[string]QueueDepth `json:"queues,omitempty"`
	UptimeSeconds float64               `json:"uptime_seconds"`
}

// DomainMetrics is the T-17 counter set. Every label here is bounded by code —
// a tool name from the registry, an action kind from the action registry, a
// model id from deployment config — because a scraped endpoint labelled by
// anything a tenant types is an unbounded allocation.
type DomainMetrics struct {
	// WatcherFires is keyed by outcome: "breached", "suppressed", "quiet".
	WatcherFires     map[string]int64       `json:"watcher_fires,omitempty"`
	ActionExecutions map[string]int64       `json:"action_executions,omitempty"`
	ActionFailures   map[string]int64       `json:"action_failures,omitempty"`
	Tools            map[string]ToolMetrics `json:"tools,omitempty"`
	Turns            DurationMetrics        `json:"turns"`
	// LLMLatency is keyed by model id.
	LLMLatency map[string]DurationMetrics `json:"llm_latency,omitempty"`
}

// ToolMetrics is one tool's traffic, recorded where the audit row is written.
type ToolMetrics struct {
	Calls  int64 `json:"calls"`
	Errors int64 `json:"errors"`
	SumMS  int64 `json:"sum_ms"`
	MaxMS  int64 `json:"max_ms"`
}

// DurationMetrics is sum/count/max — enough for a mean and a worst case, which
// is what these are read for. The `/v1` route histogram keeps its buckets
// because a latency SLO is stated against it; nothing states one against a
// turn.
type DurationMetrics struct {
	Count int64 `json:"count"`
	SumMS int64 `json:"sum_ms"`
	MaxMS int64 `json:"max_ms"`
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

// QueueDepth is one asynq queue as Redis holds it at a moment: how much work
// is waiting, how much is running, and how much has fallen back to be retried
// or is scheduled for later. Pending rising while active stays flat is the
// shape that means "add a worker"; retry rising is the shape that means
// "something downstream is failing".
type QueueDepth struct {
	Pending   int64 `json:"pending"`
	Active    int64 `json:"active"`
	Scheduled int64 `json:"scheduled"`
	Retry     int64 `json:"retry"`
	Archived  int64 `json:"archived"`
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
