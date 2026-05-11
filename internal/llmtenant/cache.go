package llmtenant

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmclient"
)

// MeteredWrap wraps a freshly-built LLM with the usage-metering layer.
// Injected so the cache stays unaware of UsageService internals and tests
// can pass an identity wrapper.
type MeteredWrap func(inner interfaces.LLM) interfaces.LLM

// ClientCache caches per-tenant LLM clients keyed by (companyID, tier).
// Mirrors db.TenantConnPool: lazy build, version-based invalidation, idle
// TTL eviction, LRU bound.
type ClientCache struct {
	resolver *Resolver
	wrap     MeteredWrap
	maxSize  int
	idleTTL  time.Duration

	mu      sync.Mutex
	entries map[string]*llmEntry
}

type llmEntry struct {
	llm        interfaces.LLM
	version    string
	lastUsedAt time.Time
}

func NewClientCache(r *Resolver, wrap MeteredWrap, maxSize int, idleTTL time.Duration) *ClientCache {
	if maxSize <= 0 {
		maxSize = 300
	}
	if idleTTL <= 0 {
		idleTTL = 30 * time.Minute
	}
	return &ClientCache{
		resolver: r,
		wrap:     wrap,
		maxSize:  maxSize,
		idleTTL:  idleTTL,
		entries:  make(map[string]*llmEntry, maxSize),
	}
}

// For returns a cached or freshly-built LLM client for (companyID, tier),
// along with the resolved profile (callers need profile.Interface to gate
// provider-specific agent options like Anthropic prompt caching).
func (c *ClientCache) For(ctx context.Context, companyID string, tier domain.LLMTier) (interfaces.LLM, *EffectiveProfile, error) {
	if companyID == "" {
		return nil, nil, errors.New("companyID is required")
	}
	profile, err := c.resolver.Resolve(ctx, companyID, tier)
	if err != nil {
		return nil, nil, err
	}
	key := llmKey(companyID, tier)

	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if e.version == profile.Version {
			e.lastUsedAt = time.Now()
			llm := e.llm
			c.mu.Unlock()
			return llm, profile, nil
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()

	raw, err := llmclient.Build(llmclient.Spec{
		Interface: profile.Interface,
		APIKey:    profile.APIKey,
		Model:     profile.Model,
		BaseURL:   profile.BaseURL,
	})
	if err != nil {
		return nil, nil, err
	}
	var wrapped interfaces.LLM = raw
	if c.wrap != nil {
		wrapped = c.wrap(raw)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		c.evictOldestLocked()
	}
	c.entries[key] = &llmEntry{llm: wrapped, version: profile.Version, lastUsedAt: time.Now()}
	return wrapped, profile, nil
}

// Invalidate drops cached entries for one company across all tiers.
func (c *ClientCache) Invalidate(companyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if hasCompanyPrefix(k, companyID) {
			delete(c.entries, k)
		}
	}
}

// Start spawns the idle reaper. Cancel ctx to stop.
func (c *ClientCache) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(c.idleTTL / 2)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.reapIdle()
			}
		}
	}()
}

// CloseAll drops every cached entry. LLM clients don't own external
// resources we must close, so this just clears the map.
func (c *ClientCache) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		delete(c.entries, k)
	}
}

// Stats reports current size + cap (for /metrics).
func (c *ClientCache) Stats() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.maxSize
}

func (c *ClientCache) evictOldestLocked() {
	var oldestKey string
	var oldestAt time.Time
	for k, e := range c.entries {
		if oldestKey == "" || e.lastUsedAt.Before(oldestAt) {
			oldestKey = k
			oldestAt = e.lastUsedAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func (c *ClientCache) reapIdle() {
	cutoff := time.Now().Add(-c.idleTTL)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if e.lastUsedAt.Before(cutoff) {
			delete(c.entries, k)
		}
	}
}

func llmKey(companyID string, tier domain.LLMTier) string {
	return companyID + ":" + string(tier)
}

func hasCompanyPrefix(key, companyID string) bool {
	prefix := companyID + ":"
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}
