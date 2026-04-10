package metrics

import (
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

	// Timestamp of last reset
	lastReset time.Time
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		lastReset: time.Now(),
	}
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
			RequestsTotal:  atomic.LoadInt64(&c.llmRequestsTotal),
			TokensInTotal:  atomic.LoadInt64(&c.llmTokensInTotal),
			TokensOutTotal: atomic.LoadInt64(&c.llmTokensOutTotal),
			CostTotal:      float64(atomic.LoadUint64(&c.llmCostTotalMicro)) / 1000000,
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

		UptimeSeconds: time.Since(c.lastReset).Seconds(),
	}
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
	atomic.StoreInt64(&c.cacheHits, 0)
	atomic.StoreInt64(&c.cacheMisses, 0)
	atomic.StoreInt64(&c.jobsCreated, 0)
	atomic.StoreInt64(&c.jobsCompleted, 0)
	atomic.StoreInt64(&c.jobsFailed, 0)
	atomic.StoreInt64(&c.contextResets, 0)

	c.mu.Lock()
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
	UptimeSeconds float64             `json:"uptime_seconds"`
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
