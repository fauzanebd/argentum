package apiobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
)

// fakeRepo records what a flush wrote.
type fakeRepo struct {
	mu       sync.Mutex
	stats    []domain.APIRequestStatRow
	errs     []domain.APIRequestError
	pruned   []time.Time
	statsErr error
	errsErr  error
}

func (f *fakeRepo) UpsertStats(_ context.Context, rows []domain.APIRequestStatRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats = append(f.stats, rows...)
	return f.statsErr
}

func (f *fakeRepo) InsertErrors(_ context.Context, rows []domain.APIRequestError) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, rows...)
	return f.errsErr
}

func (f *fakeRepo) StatsByKey(context.Context, string, time.Time) (map[string]*domain.APIKeyRequestStats, error) {
	return nil, nil
}

func (f *fakeRepo) RecentErrors(context.Context, string, string, int) ([]*domain.APIRequestError, error) {
	return nil, nil
}

func (f *fakeRepo) Prune(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruned = append(f.pruned, before)
	return 0, nil
}

func (f *fakeRepo) written() ([]domain.APIRequestStatRow, []domain.APIRequestError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats, f.errs
}

func sample(status int, mods ...func(*domain.APIRequestSample)) domain.APIRequestSample {
	s := domain.APIRequestSample{
		CompanyID: "co-1",
		APIKeyID:  "key-1",
		RequestID: "req_abc",
		Method:    "GET",
		Route:     "/v1/me",
		Status:    status,
		Latency:   40 * time.Millisecond,
		At:        time.Date(2026, 7, 30, 10, 30, 0, 0, time.UTC),
	}
	for _, m := range mods {
		m(&s)
	}
	return s
}

// TestBucketsFoldByHour is the rollup's whole point: many requests, one row.
func TestBucketsFoldByHour(t *testing.T) {
	repo := &fakeRepo{}
	r := New(repo, nil)

	for range 5 {
		r.Record(sample(200))
	}
	// Same hour, different minute — still one bucket.
	r.Record(sample(200, func(s *domain.APIRequestSample) {
		s.At = time.Date(2026, 7, 30, 10, 59, 59, 0, time.UTC)
		s.Latency = 900 * time.Millisecond
	}))
	// Next hour is a second bucket, because "did it break after the deploy?" is
	// the question the bucket exists to answer.
	r.Record(sample(200, func(s *domain.APIRequestSample) {
		s.At = time.Date(2026, 7, 30, 11, 0, 1, 0, time.UTC)
	}))

	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	stats, _ := repo.written()
	if len(stats) != 2 {
		t.Fatalf("wrote %d rows, want 2 (one per hour)", len(stats))
	}
	var first domain.APIRequestStatRow
	for _, row := range stats {
		if row.BucketHour.Hour() == 10 {
			first = row
		}
	}
	if first.Requests != 6 {
		t.Errorf("requests = %d, want 6", first.Requests)
	}
	if first.LatencyMSMax != 900 {
		t.Errorf("latency max = %d, want 900", first.LatencyMSMax)
	}
	if want := int64(5*40 + 900); first.LatencyMSSum != want {
		t.Errorf("latency sum = %d, want %d", first.LatencyMSSum, want)
	}
	if first.StatusClass != 2 {
		t.Errorf("status class = %d, want 2", first.StatusClass)
	}
}

// TestOnlyFailuresKeepDetail is the other half of the two-table decision: a
// 200 is a counter, a 403 is a row somebody reads.
func TestOnlyFailuresKeepDetail(t *testing.T) {
	repo := &fakeRepo{}
	r := New(repo, nil)

	r.Record(sample(200))
	r.Record(sample(403, func(s *domain.APIRequestSample) {
		s.ErrorCode = "insufficient_scope"
		s.ErrorType = "permission"
		s.RequestID = "req_denied"
	}))
	r.Record(sample(429, func(s *domain.APIRequestSample) {
		s.ErrorCode = "rate_limit_exceeded"
		s.ErrorType = "rate_limit"
	}))

	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_, errs := repo.written()
	if len(errs) != 2 {
		t.Fatalf("recorded %d failures, want 2", len(errs))
	}
	byCode := map[string]domain.APIRequestError{}
	for _, e := range errs {
		byCode[e.ErrorCode] = e
	}
	denied, ok := byCode["insufficient_scope"]
	if !ok {
		t.Fatal("the 403 was not recorded with its code")
	}
	// The request id is the only handle the caller has. If it does not survive
	// into the row, the tab cannot answer the question the caller is asking.
	if denied.RequestID != "req_denied" {
		t.Errorf("request id = %q, want req_denied", denied.RequestID)
	}
	if denied.Status != 403 || denied.ErrorType != "permission" {
		t.Errorf("recorded %d/%q, want 403/permission", denied.Status, denied.ErrorType)
	}
	if _, ok := byCode["rate_limit_exceeded"]; !ok {
		t.Error("the 429 was not recorded; a rate-limited integrator has nothing to look at")
	}
}

// TestUnauthenticatedSamplesAreNotPersisted covers the deliberate gap: a 401
// from an unknown credential belongs to no tenant, and the two alternatives are
// guessing whose it was or showing it to all of them.
func TestUnauthenticatedSamplesAreNotPersisted(t *testing.T) {
	repo := &fakeRepo{}
	coll := metrics.NewCollector()
	r := New(repo, coll)

	r.Record(sample(401, func(s *domain.APIRequestSample) {
		s.CompanyID = ""
		s.APIKeyID = ""
		s.ErrorCode = "invalid_api_key"
	}))

	if buckets, errs, _ := r.Buffered(); buckets != 0 || errs != 0 {
		t.Fatalf("buffered %d buckets and %d errors, want none", buckets, errs)
	}
	// It is still counted where an operator can see it.
	snap := coll.GetSnapshot()
	route, ok := snap.APIV1.Routes["GET /v1/me"]
	if !ok {
		t.Fatal("the unauthenticated request was not counted on /metrics either")
	}
	if route.Requests != 1 || route.Errors != 1 {
		t.Errorf("route counted %d requests / %d errors, want 1/1", route.Requests, route.Errors)
	}
	if len(snap.APIV1.Keys) != 0 {
		t.Errorf("a request with no key produced %d key labels", len(snap.APIV1.Keys))
	}
}

// TestErrorBufferIsCapped keeps a tenant's retry storm from becoming our memory
// usage, and makes the truncation countable rather than silent.
func TestErrorBufferIsCapped(t *testing.T) {
	repo := &fakeRepo{}
	r := New(repo, nil)
	r.maxErrs = 3

	for range 10 {
		r.Record(sample(500))
	}
	_, buffered, dropped := r.Buffered()
	if buffered != 3 {
		t.Errorf("buffered %d failures, want the cap of 3", buffered)
	}
	if dropped != 7 {
		t.Errorf("dropped = %d, want 7", dropped)
	}
	// The counters are unaffected: the rollup is a per-bucket increment, so it
	// carries all ten even when the detail does not.
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	stats, errs := repo.written()
	if len(stats) != 1 || stats[0].Requests != 10 {
		t.Errorf("rollup lost requests to the error cap: %+v", stats)
	}
	if len(errs) != 3 {
		t.Errorf("wrote %d failures, want 3", len(errs))
	}
	if _, _, dropped := r.Buffered(); dropped != 0 {
		t.Errorf("dropped counter was not cleared by the flush: %d", dropped)
	}
}

// TestFlushDropsABatchItCannotWrite pins the trade the package doc states: a
// failed write loses the batch instead of growing a queue across an outage of
// unknown length.
func TestFlushDropsABatchItCannotWrite(t *testing.T) {
	repo := &fakeRepo{statsErr: errors.New("connection refused")}
	r := New(repo, nil)
	r.Record(sample(200))

	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("Flush returned nil after a failing write")
	}
	if buckets, _, _ := r.Buffered(); buckets != 0 {
		t.Errorf("%d buckets survived a failed flush; the buffer must not grow across an outage", buckets)
	}
}

// TestNilRepoStillFeedsMetrics is what makes it safe to install the middleware
// unconditionally on a deployment without the 032 migration.
func TestNilRepoStillFeedsMetrics(t *testing.T) {
	coll := metrics.NewCollector()
	r := New(nil, coll)
	r.Record(sample(200))

	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush with no repository: %v", err)
	}
	if got := coll.GetSnapshot().APIV1.Routes["GET /v1/me"].Requests; got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// TestPruneIsThrottled: retention is enforced on a flush, but not on every one.
func TestPruneIsThrottled(t *testing.T) {
	repo := &fakeRepo{}
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	r := New(repo, nil, WithClock(func() time.Time { return now }), WithRetention(48*time.Hour))

	r.Record(sample(200))
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	r.Record(sample(200))
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	repo.mu.Lock()
	pruned := append([]time.Time(nil), repo.pruned...)
	repo.mu.Unlock()
	if len(pruned) != 1 {
		t.Fatalf("pruned %d times in one hour, want 1", len(pruned))
	}
	if want := now.Add(-48 * time.Hour); !pruned[0].Equal(want) {
		t.Errorf("pruned before %s, want %s", pruned[0], want)
	}

	now = now.Add(2 * time.Hour)
	r.Record(sample(200))
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	repo.mu.Lock()
	n := len(repo.pruned)
	repo.mu.Unlock()
	if n != 2 {
		t.Errorf("pruned %d times after two hours, want 2", n)
	}
}

// TestConcurrentRecordAndFlush is the property the request path depends on:
// requests keep being recorded while a flush is writing.
func TestConcurrentRecordAndFlush(t *testing.T) {
	repo := &fakeRepo{}
	r := New(repo, metrics.NewCollector())

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 50 {
				r.Record(sample(200 + i))
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10 {
			_ = r.Flush(context.Background())
		}
	}()
	wg.Wait()
	_ = r.Flush(context.Background())

	stats, _ := repo.written()
	var total int64
	for _, row := range stats {
		total += row.Requests
	}
	if total != 400 {
		t.Errorf("recorded %d requests across flushes, want 400", total)
	}
}

// TestStatusClassification pins the mapping every counter and every error-rate
// depends on, including the 3xx that /v1 should never issue.
func TestStatusClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  int
		failed bool
	}{
		{200, 2, false},
		{202, 2, false},
		{204, 2, false},
		{302, 3, true},
		{400, 4, true},
		{403, 4, true},
		{429, 4, true},
		{500, 5, true},
		{503, 5, true},
		{0, 5, true},
	} {
		s := domain.APIRequestSample{Status: tc.status}
		if got := s.StatusClass(); got != tc.class {
			t.Errorf("status %d: class = %d, want %d", tc.status, got, tc.class)
		}
		if got := s.Failed(); got != tc.failed {
			t.Errorf("status %d: failed = %v, want %v", tc.status, got, tc.failed)
		}
	}
}
