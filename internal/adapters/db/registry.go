package db

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

var (
	registryMu sync.RWMutex
	registry   = map[string]Driver{}
)

// Register installs a Driver into the package-level registry. Drivers should
// call this from their package init() so the binary picks them up via blank
// import.
func Register(d Driver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[d.Type()] = d
}

// Get returns the Driver registered for the given type.
func Get(t string) (Driver, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[t]
	if !ok {
		return nil, fmt.Errorf("db driver %q not registered", t)
	}
	return d, nil
}

// Registered returns the sorted list of currently-registered driver types.
// Used by the `GET /api/meta/supported-databases` endpoint so the frontend
// only shows drivers that are actually wired in.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PingDSN opens a temporary connection through the named driver, pings it,
// and closes it. Used by the dashboard's "Test connection" button.
func PingDSN(ctx context.Context, dbType, dsn string) error {
	d, err := Get(dbType)
	if err != nil {
		return err
	}
	conn, err := d.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer conn.Close()
	if err := conn.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}
