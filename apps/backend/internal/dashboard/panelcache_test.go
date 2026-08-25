package dashboard

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// The key must change when anything that can change the answer changes, and
// connVersion is the one that is easy to leave out.
func TestSQLPanelKeyCoversEveryInputThatChangesTheAnswer(t *testing.T) {
	base := func() []any {
		return []any{"co", "src", "v1", "postgres", "select 1", `["a"]`, 100}
	}
	key := func(a []any) string {
		return SQLPanelKey(a[0].(string), a[1].(string), a[2].(string), a[3].(string), a[4].(string), a[5].(string), a[6].(int))
	}
	original := key(base())

	for i, name := range []string{"companyID", "sourceID", "connVersion", "dbType", "sql", "args", "maxRows"} {
		a := base()
		if i == 6 {
			a[i] = 200
		} else {
			a[i] = a[i].(string) + "-changed"
		}
		if got := key(a); got == original {
			t.Errorf("changing %s did not change the key — that input is not in it", name)
		}
	}

	if key(base()) != original {
		t.Error("the same inputs must produce the same key")
	}
}

// A cache keyed only on SQL would go on serving a database the tenant
// disconnected. This is that property, named.
func TestARotatedConnectionGetsADifferentKey(t *testing.T) {
	before := SQLPanelKey("co", "src", "version-1", "postgres", "select sum(x) from t", "", 100)
	after := SQLPanelKey("co", "src", "version-2", "postgres", "select sum(x) from t", "", 100)
	if before == after {
		t.Fatal("a rotated DSN must not read the old warehouse's cached numbers")
	}
}

// Components are joined on a separator no SQL literal can forge, so two
// different panels cannot collide by containing the delimiter.
func TestKeyComponentsCannotForgeABoundary(t *testing.T) {
	a := SQLPanelKey("co", "src", "v1", "postgres", "select 'x'", "y", 100)
	b := SQLPanelKey("co", "src", "v1", "postgres", "select 'x'|y", "", 100)
	if a == b {
		t.Error("a literal containing the delimiter collided with a different panel")
	}
}

func TestMetricPanelKeyIsTheMetricAndTheWindow(t *testing.T) {
	from := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	base := MetricPanelKey("co", "revenue", from, to)

	if MetricPanelKey("co", "orders", from, to) == base {
		t.Error("a different metric must not share a key")
	}
	if MetricPanelKey("co", "revenue", from, to.AddDate(0, 0, 1)) == base {
		t.Error("a different window must not share a key")
	}
	if MetricPanelKey("other", "revenue", from, to) == base {
		t.Error("a different company must not share a key")
	}
}

// A nil cache is a working cache that never caches, so a deployment with no
// Redis needs no branch anywhere else.
func TestNilCacheStillRunsTheQuery(t *testing.T) {
	var c *PanelCache
	got, outcome, err := c.Do(context.Background(), "k", func() ([]byte, error) {
		return []byte("fresh"), nil
	})
	if err != nil || string(got) != "fresh" {
		t.Fatalf("got %q err %v", got, err)
	}
	if outcome != OutcomeMiss {
		t.Errorf("outcome = %v, want miss", outcome)
	}
}

// The thundering herd is the failure that is real on day one: twenty people
// open the same dashboard at 09:00 and twelve panels each run twenty times
// against a customer's warehouse.
func TestConcurrentCallersCollapseIntoOneExecution(t *testing.T) {
	c := &PanelCache{ttl: time.Minute, calls: make(map[string]*inflight)}

	var (
		mu    sync.Mutex
		execs int
	)
	release := make(chan struct{})
	run := func() ([]byte, error) {
		mu.Lock()
		execs++
		mu.Unlock()
		<-release // hold the leader open so the others pile up behind it
		return []byte("value"), nil
	}

	const callers = 20
	var wg sync.WaitGroup
	outcomes := make([]Outcome, callers)
	values := make([][]byte, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, o, err := c.Do(context.Background(), "same-key", run)
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
			values[i], outcomes[i] = v, o
		}(i)
	}
	// Give the goroutines time to arrive before the leader returns.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if execs != 1 {
		t.Errorf("the warehouse was read %d times for %d simultaneous callers, want 1", execs, callers)
	}

	var misses, collapsed int
	for i, o := range outcomes {
		if string(values[i]) != "value" {
			t.Errorf("caller %d got %q, want every caller to receive the leader's answer", i, values[i])
		}
		switch o {
		case OutcomeMiss:
			misses++
		case OutcomeCollapsed:
			collapsed++
		}
	}
	// Exactly one miss, because T-D9 writes a query-log row per miss: a
	// collapsed wait read nothing and must leave no row.
	if misses != 1 {
		t.Errorf("misses = %d, want exactly 1 — the query log writes one row per execution", misses)
	}
	if collapsed != callers-1 {
		t.Errorf("collapsed = %d, want %d", collapsed, callers-1)
	}
}

// A failing execution must not be cached, and must not leave the key wedged
// so that every later caller waits on a dead leader.
func TestAFailedExecutionIsNotCachedAndReleasesTheKey(t *testing.T) {
	c := &PanelCache{ttl: time.Minute, calls: make(map[string]*inflight)}
	boom := fmt.Errorf("warehouse said no")

	if _, _, err := c.Do(context.Background(), "k", func() ([]byte, error) { return nil, boom }); err != boom {
		t.Fatalf("err = %v, want the execution's own error", err)
	}
	c.mu.Lock()
	n := len(c.calls)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("%d in-flight entries left behind; the next caller would wait forever", n)
	}

	got, _, err := c.Do(context.Background(), "k", func() ([]byte, error) { return []byte("ok now"), nil })
	if err != nil || string(got) != "ok now" {
		t.Errorf("a later caller must run its own execution; got %q err %v", got, err)
	}
}
