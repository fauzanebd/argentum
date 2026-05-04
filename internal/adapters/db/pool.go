package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ConnectionResolver is the minimum control-plane lookup the pool needs to
// turn a company ID into the DSN + db_type to dial. The control-DB
// ConnectionRepository in internal/adapters/postgres satisfies this once
// wrapped with a thin DSN-decryption layer; for tests, a fake suffices.
type ConnectionResolver interface {
	Resolve(ctx context.Context, companyID string) (dbType, dsn, version string, err error)
}

// TenantConnPool keeps a small LRU of live tenant Conns keyed by company ID.
// It is safe for concurrent use.
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

// For returns a live Conn for the given company, opening one if needed.
func (p *TenantConnPool) For(ctx context.Context, companyID string) (Conn, error) {
	if companyID == "" {
		return nil, errors.New("companyID is required")
	}

	dbType, dsn, version, err := p.resolver.Resolve(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant connection: %w", err)
	}

	p.mu.Lock()
	if e, ok := p.entries[companyID]; ok {
		if e.version == version && e.dbType == dbType {
			e.lastUsedAt = time.Now()
			conn := e.conn
			p.mu.Unlock()
			return conn, nil
		}
		_ = e.conn.Close()
		delete(p.entries, companyID)
	}
	p.mu.Unlock()

	driver, err := Get(dbType)
	if err != nil {
		return nil, err
	}
	conn, err := driver.Open(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open tenant connection: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) >= p.maxSize {
		p.evictOldestLocked()
	}
	p.entries[companyID] = &tenantEntry{
		conn:       conn,
		version:    version,
		dbType:     dbType,
		lastUsedAt: time.Now(),
	}
	return conn, nil
}

// Invalidate forcibly drops the cached connection for a company (e.g. after
// the user updates their DSN in the dashboard).
func (p *TenantConnPool) Invalidate(companyID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[companyID]; ok {
		_ = e.conn.Close()
		delete(p.entries, companyID)
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
