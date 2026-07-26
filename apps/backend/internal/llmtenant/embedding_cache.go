package llmtenant

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/embedding"
)

// EmbeddingCache caches per-tenant embedding clients. Same TTL/LRU shape as
// ClientCache, but for embedding.Client. Returns nil client (no error) when
// the resolved profile has no API key — callers branch on nil to silent-skip
// embedding work, preserving today's behavior when env keys aren't set.
type EmbeddingCache struct {
	resolver *Resolver
	maxSize  int
	idleTTL  time.Duration

	mu      sync.Mutex
	entries map[string]*embedEntry
}

type embedEntry struct {
	client     embedding.Client // nil = "tenant has no embedding setup"
	version    string
	lastUsedAt time.Time
}

func NewEmbeddingCache(r *Resolver, maxSize int, idleTTL time.Duration) *EmbeddingCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	if idleTTL <= 0 {
		idleTTL = 30 * time.Minute
	}
	return &EmbeddingCache{
		resolver: r,
		maxSize:  maxSize,
		idleTTL:  idleTTL,
		entries:  make(map[string]*embedEntry, maxSize),
	}
}

// For returns the cached or freshly-built embedding client for a company.
// Returns (nil, nil) when the resolved profile has no API key — caller
// silent-skips (matches today's embedding.Build behavior).
func (c *EmbeddingCache) For(ctx context.Context, companyID string) (embedding.Client, error) {
	if companyID == "" {
		return nil, errors.New("companyID is required")
	}
	profile, err := c.resolver.Resolve(ctx, companyID, domain.LLMTierEmbedding)
	if err != nil {
		return nil, err
	}
	key := companyID

	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if e.version == profile.Version {
			e.lastUsedAt = time.Now()
			cli := e.client
			c.mu.Unlock()
			return cli, nil
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()

	built := embedding.BuildForProfile(embedding.ProfileSpec{
		Provider:  profile.Interface,
		APIKey:    profile.APIKey,
		BaseURL:   profile.BaseURL,
		Model:     profile.Model,
		Dim:       profile.Dim,
		BatchSize: profile.BatchSize,
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		c.evictOldestLocked()
	}
	c.entries[key] = &embedEntry{client: built, version: profile.Version, lastUsedAt: time.Now()}
	return built, nil
}

func (c *EmbeddingCache) Invalidate(companyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, companyID)
}

func (c *EmbeddingCache) Start(ctx context.Context) {
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

func (c *EmbeddingCache) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		delete(c.entries, k)
	}
}

func (c *EmbeddingCache) evictOldestLocked() {
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

func (c *EmbeddingCache) reapIdle() {
	cutoff := time.Now().Add(-c.idleTTL)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if e.lastUsedAt.Before(cutoff) {
			delete(c.entries, k)
		}
	}
}
