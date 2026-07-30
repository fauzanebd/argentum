package metrics

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestAPIRequestHistogramIsCumulative pins the shape a quantile is read out of.
// A per-bucket count and a cumulative one look identical in a snapshot of one
// request and disagree about everything after that.
func TestAPIRequestHistogramIsCumulative(t *testing.T) {
	c := NewCollector()
	for _, ms := range []int{1, 7, 30, 300, 400_000} {
		c.RecordAPIRequest("GET", "/v1/me", "key-1", 200, time.Duration(ms)*time.Millisecond)
	}

	route, ok := c.GetSnapshot().APIV1.Routes["GET /v1/me"]
	if !ok {
		t.Fatal("no route entry")
	}
	if route.Latency.Count != 5 {
		t.Errorf("count = %d, want 5", route.Latency.Count)
	}
	if route.Latency.SumMS != 1+7+30+300+400_000 {
		t.Errorf("sum = %d", route.Latency.SumMS)
	}
	if route.Latency.MaxMS != 400_000 {
		t.Errorf("max = %d, want 400000", route.Latency.MaxMS)
	}
	for bound, want := range map[string]int64{
		"5":      1, // 1ms
		"10":     2, // + 7ms
		"50":     3, // + 30ms
		"500":    4, // + 300ms
		"120000": 4, // the 400s outlier is over every declared bound
		"+Inf":   5, // + that outlier
	} {
		if got := route.Latency.Buckets[bound]; got != want {
			t.Errorf("bucket le=%s = %d, want %d", bound, got, want)
		}
	}
}

// TestAPIRequestCountsByStatus is what makes a 403 distinguishable from a 429 on
// the endpoint — the reason exact statuses are kept here and classes are what
// the durable rollup stores.
func TestAPIRequestCountsByStatus(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("POST", "/v1/chat", "key-1", 200, time.Millisecond)
	c.RecordAPIRequest("POST", "/v1/chat", "key-1", 403, time.Millisecond)
	c.RecordAPIRequest("POST", "/v1/chat", "key-2", 429, time.Millisecond)

	snap := c.GetSnapshot()
	route := snap.APIV1.Routes["POST /v1/chat"]
	if route.Requests != 3 || route.Errors != 2 {
		t.Errorf("requests/errors = %d/%d, want 3/2", route.Requests, route.Errors)
	}
	if route.ByStatus["403"] != 1 || route.ByStatus["429"] != 1 || route.ByStatus["200"] != 1 {
		t.Errorf("by_status = %v", route.ByStatus)
	}
	if got := snap.APIV1.Keys["key-1"]; got.Requests != 2 || got.Errors != 1 {
		t.Errorf("key-1 = %+v, want 2 requests / 1 error", got)
	}
	if got := snap.APIV1.Keys["key-2"]; got.Requests != 1 || got.Errors != 1 {
		t.Errorf("key-2 = %+v, want 1/1", got)
	}
}

// TestUnmatchedRouteIsFoldedTogether: without this, a scanner walking a
// thousand URLs mints a thousand labels.
func TestUnmatchedRouteIsFoldedTogether(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("GET", "", "", 404, time.Millisecond)
	c.RecordAPIRequest("GET", "", "", 404, time.Millisecond)

	routes := c.GetSnapshot().APIV1.Routes
	if len(routes) != 1 {
		t.Fatalf("%d route labels, want 1: %v", len(routes), routes)
	}
	if routes["GET unmatched"].Requests != 2 {
		t.Errorf("unmatched = %+v", routes["GET unmatched"])
	}
}

// TestKeyCardinalityIsBounded covers the memory bound, and that exceeding it is
// countable rather than silent.
func TestKeyCardinalityIsBounded(t *testing.T) {
	c := NewCollector()
	for i := range maxTrackedAPIKeys + 25 {
		c.RecordAPIRequest("GET", "/v1/me", keyID(i), 200, time.Millisecond)
	}
	snap := c.GetSnapshot()
	if len(snap.APIV1.Keys) != maxTrackedAPIKeys {
		t.Errorf("tracked %d keys, want the cap of %d", len(snap.APIV1.Keys), maxTrackedAPIKeys)
	}
	if snap.APIV1.KeysUntracked != 25 {
		t.Errorf("keys_untracked = %d, want 25", snap.APIV1.KeysUntracked)
	}
	// The route totals are unaffected: the cap is on labels, not on counting.
	if got := snap.APIV1.Routes["GET /v1/me"].Requests; got != int64(maxTrackedAPIKeys+25) {
		t.Errorf("route requests = %d, want %d", got, maxTrackedAPIKeys+25)
	}
}

// TestWithoutKeyLabelsStripsTheTenantIdentifiers is the guard on an endpoint
// with no credential of its own. Route numbers name no tenant; key ids do.
func TestWithoutKeyLabelsStripsTheTenantIdentifiers(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("GET", "/v1/me", "key-secret", 200, time.Millisecond)

	full := c.GetSnapshot()
	if len(full.APIV1.Keys) != 1 {
		t.Fatal("the authorized snapshot should carry the key block")
	}
	stripped := full.WithoutKeyLabels()
	if len(stripped.APIV1.Keys) != 0 || stripped.APIV1.KeysUntracked != 0 {
		t.Errorf("key labels survived stripping: %+v", stripped.APIV1)
	}
	if len(stripped.APIV1.Routes) != 1 {
		t.Error("stripping removed the route block too; route numbers name no tenant")
	}
	// And the original is untouched — the snapshot is a value, not a view.
	if len(full.APIV1.Keys) != 1 {
		t.Error("WithoutKeyLabels mutated the snapshot it was called on")
	}
}

// TestSnapshotIsACopy: a scrape must not hand out a map that request goroutines
// are still writing. Run with -race, this fails loudly if it ever does.
func TestSnapshotIsACopy(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			c.RecordAPIRequest("GET", "/v1/me", keyID(i%10), 200, time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			snap := c.GetSnapshot()
			for _, r := range snap.APIV1.Routes {
				_ = r.Requests
			}
			for _, k := range snap.APIV1.Keys {
				_ = k.Requests
			}
		}
	}()
	wg.Wait()
}

func keyID(i int) string { return "key-" + strconv.Itoa(i) }
