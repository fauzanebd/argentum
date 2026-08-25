package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ConnectionResolver is the minimum control-plane lookup the pool needs to
// turn a (companyID, sourceID) pair into the DSN + db_type to dial. Empty
// sourceID resolves to the company's default connection. The resolver returns
// the row's actual ID as resolvedSourceID; the pool keys its cache by that ID
// so empty-string lookups share an entry with explicit-id lookups for the
// same underlying connection.
//
// The control-DB ConnectionRepository in internal/adapters/postgres satisfies
// this once wrapped with a thin DSN-decryption layer; for tests, a fake
// suffices.
type ConnectionResolver interface {
	Resolve(ctx context.Context, companyID, sourceID string) (resolvedSourceID, dbType, dsn, version string, err error)
}

// TenantConnPool keeps a small LRU of live tenant Conns keyed by
// `companyID:sourceID`. It is safe for concurrent use.
//
//   - Hot path (cache hit): one map lookup, no locking beyond the rwmutex.
//   - Cold path (cache miss): resolve DSN from ConnectionResolver, dial via
//     the registered Driver, install in the cache.
//   - Eviction: idle entries past `idleTTL` are closed by the janitor; the
//     LRU bound caps total open tenants.
//
// `version` is an opaque token from the resolver. When the control-plane
// connection record is updated (DSN rotated, db_type changed), its version
// changes. The pool compares versions on every hit and re-dials if stale.
type TenantConnPool struct {
	resolver ConnectionResolver
	maxSize  int
	idleTTL  time.Duration

	mu      sync.Mutex
	entries map[string]*tenantEntry
}

type tenantEntry struct {
	conn       Conn
	version    string
	dbType     string
	lastUsedAt time.Time
}

// NewTenantConnPool constructs an empty pool. Call Start to spawn the idle
// reaper.
func NewTenantConnPool(resolver ConnectionResolver, maxSize int, idleTTL time.Duration) *TenantConnPool {
	if maxSize <= 0 {
		maxSize = 200
	}
	if idleTTL <= 0 {
		idleTTL = 30 * time.Minute
	}
	return &TenantConnPool{
		resolver: resolver,
		maxSize:  maxSize,
		idleTTL:  idleTTL,
		entries:  make(map[string]*tenantEntry, maxSize),
	}
}

func cacheKey(companyID, sourceID string) string {
	return companyID + ":" + sourceID
}

// ConnMeta is what the pool knows about a connection besides the connection:
// which record it resolved to, which driver it speaks, and the opaque version
// token that changes when the control-plane record does.
//
// It exists because a cache keyed on SQL alone is wrong. A rotated DSN can
// point at an entirely different database, and a panel cache that did not carry
// `Version` would keep serving the old warehouse's numbers under the new
// connection — the failure being read as "the dashboard is stale" while it is
// actually "the dashboard is answering from a database you disconnected"
// (T-D8).
type ConnMeta struct {
	// SourceID is the resolved id, never empty — callers that passed "" for
	// the company default get the real row's id back and can key on it.
	SourceID string
	DBType   string
	Version  string
}

// For returns a live Conn for the given (company, source). Empty sourceID
// resolves to the company's default connection; the pool keys the cache by
// the resolver-returned ID so default-by-empty and explicit-id share an entry.
//
// Signature deliberately unchanged by T-D8: MetricService depends on a narrowed
// interface with exactly this method (internal/app/metric_service.go), and that
// is the interface that would break. ForWithMeta is the addition; this
// delegates to it.
func (p *TenantConnPool) For(ctx context.Context, companyID, sourceID string) (Conn, error) {
	conn, _, err := p.ForWithMeta(ctx, companyID, sourceID)
	return conn, err
}

// ForWithMeta is For, and also says which connection answered.
func (p *TenantConnPool) ForWithMeta(ctx context.Context, companyID, sourceID string) (Conn, ConnMeta, error) {
	if companyID == "" {
		return nil, ConnMeta{}, errors.New("companyID is required")
	}

	resolvedID, dbType, dsn, version, err := p.resolver.Resolve(ctx, companyID, sourceID)
	if err != nil {
		return nil, ConnMeta{}, fmt.Errorf("resolve tenant connection: %w", err)
	}
	meta := ConnMeta{SourceID: resolvedID, DBType: dbType, Version: version}

	key := cacheKey(companyID, resolvedID)

	p.mu.Lock()
	if e, ok := p.entries[key]; ok {
		if e.version == version && e.dbType == dbType {
			e.lastUsedAt = time.Now()
			conn := e.conn
			p.mu.Unlock()
			return conn, meta, nil
		}
		_ = e.conn.Close()
		delete(p.entries, key)
	}
	p.mu.Unlock()

	driver, err := Get(dbType)
	if err != nil {
		return nil, ConnMeta{}, err
	}
	conn, err := driver.Open(ctx, dsn)
	if err != nil {
		return nil, ConnMeta{}, fmt.Errorf("open tenant connection: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) >= p.maxSize {
		p.evictOldestLocked()
	}
	p.entries[key] = &tenantEntry{
		conn:       conn,
		version:    version,
		dbType:     dbType,
		lastUsedAt: time.Now(),
	}
	return conn, meta, nil
}

// Invalidate forcibly drops the cached connection for one (company, source).
// sourceID must be the resolved (non-empty) ID.
func (p *TenantConnPool) Invalidate(companyID, sourceID string) {
	key := cacheKey(companyID, sourceID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[key]; ok {
		_ = e.conn.Close()
		delete(p.entries, key)
	}
}

// InvalidateAll drops every cached connection for a company. Used when a
// company-wide event (default switch, key rotation) makes per-source
// invalidation insufficient.
func (p *TenantConnPool) InvalidateAll(companyID string) {
	prefix := companyID + ":"
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.entries {
		if strings.HasPrefix(k, prefix) {
			_ = e.conn.Close()
			delete(p.entries, k)
		}
	}
}

// Stats reports the current pool size — useful for /metrics.
func (p *TenantConnPool) Stats() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries), p.maxSize
}

// Start spawns a background goroutine that periodically closes connections
// idle longer than idleTTL. Cancel ctx to stop the janitor.
func (p *TenantConnPool) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(p.idleTTL / 2)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.reapIdle()
			}
		}
	}()
}

// CloseAll evicts every connection. Call from main on shutdown.
func (p *TenantConnPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.entries {
		_ = e.conn.Close()
		delete(p.entries, k)
	}
}

func (p *TenantConnPool) evictOldestLocked() {
	var oldestKey string
	var oldestAt time.Time
	for k, e := range p.entries {
		if oldestKey == "" || e.lastUsedAt.Before(oldestAt) {
			oldestKey = k
			oldestAt = e.lastUsedAt
		}
	}
	if oldestKey != "" {
		_ = p.entries[oldestKey].conn.Close()
		delete(p.entries, oldestKey)
	}
}

func (p *TenantConnPool) reapIdle() {
	cutoff := time.Now().Add(-p.idleTTL)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.entries {
		if e.lastUsedAt.Before(cutoff) {
			_ = e.conn.Close()
			delete(p.entries, k)
		}
	}
}
