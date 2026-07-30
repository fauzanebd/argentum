// Package apiobs turns finished `/v1` requests into something the tenant can
// read (T-A5).
//
// An integrator debugging a 403 at 11pm should not need us to read a log. That
// is the whole design constraint, and it has two consequences that shape this
// package:
//
//   - The record has to be **theirs**, so it is company-scoped and served from
//     their own dashboard rather than from our observability stack.
//   - It must not cost the request that produced it. A machine API is called in
//     loops, and a Postgres round trip per call to record that the call
//     happened is a latency tax on every integration we have.
//
// So the request path does a map update under a mutex and nothing else, and a
// flush loop writes batches. The cost of that is stated plainly: up to one
// flush interval of records is lost if the process dies, and a flush that fails
// drops its batch rather than growing a queue. Losing observability is the
// right thing to lose in both cases — the alternative is an unbounded buffer
// that turns a Postgres outage into an API outage.
package apiobs

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
)

const (
	// DefaultFlushInterval is well inside the ticket's "within a minute", with
	// room for a flush to fail and the next one to carry the difference.
	DefaultFlushInterval = 15 * time.Second
	// DefaultRetention bounds both tables. A month is longer than any debugging
	// session and short enough that the rollup stays small.
	DefaultRetention = 30 * 24 * time.Hour
	// maxBufferedErrors caps the failure detail held between flushes. A tenant
	// whose script is failing in a tight loop would otherwise turn our memory
	// into their retry budget. Overflow is counted and logged, never silently
	// dropped — see Recorder.Flush.
	maxBufferedErrors = 1000
	// pruneEvery throttles retention. It is deliberately not tied to the flush
	// interval: a DELETE over a month-old window every 15 seconds is work for
	// no reason.
	pruneEvery = time.Hour
)

// Recorder aggregates samples and flushes them. Safe for concurrent use; every
// exported method may be called from any request goroutine.
type Recorder struct {
	repo    domain.APIRequestRepository
	coll    *metrics.Collector
	now     func() time.Time
	retain  time.Duration
	maxErrs int

	mu        sync.Mutex
	buckets   map[bucketKey]*bucketAgg
	errs      []domain.APIRequestError
	dropped   int64
	lastPrune time.Time
}

// bucketKey is the rollup's primary key, minus the counters.
type bucketKey struct {
	companyID   string
	apiKeyID    string
	hour        time.Time
	route       string
	method      string
	statusClass int
}

type bucketAgg struct {
	requests int64
	latSumMS int64
	latMaxMS int
}

// Option configures a Recorder. Both knobs exist because the flush interval is
// the one thing an operator may reasonably want to change (a busy deployment
// trades staleness for fewer writes) and retention is the one thing a tenant's
// support agreement may.
type Option func(*Recorder)

// WithRetention overrides how long rows are kept.
func WithRetention(d time.Duration) Option {
	return func(r *Recorder) {
		if d > 0 {
			r.retain = d
		}
	}
}

// WithClock overrides the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(r *Recorder) {
		if now != nil {
			r.now = now
		}
	}
}

// New builds a Recorder. repo may be nil — on a deployment without the
// migration applied, or in a stripped-down wiring — and the recorder then keeps
// the /metrics side working and persists nothing, which is what makes it safe
// to install the middleware unconditionally.
func New(repo domain.APIRequestRepository, coll *metrics.Collector, opts ...Option) *Recorder {
	r := &Recorder{
		repo:    repo,
		coll:    coll,
		now:     time.Now,
		retain:  DefaultRetention,
		maxErrs: maxBufferedErrors,
		buckets: map[bucketKey]*bucketAgg{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Record accepts one finished request. It never blocks on I/O and never
// returns an error: nothing about recording a request may change how that
// request was answered.
//
// A sample with no company or no key is counted on /metrics and not persisted.
// That is the 401 case — an unknown credential belongs to no tenant, and the
// two dishonest alternatives are guessing whose it was or showing it to
// everyone. The server log keeps those, which is where a credential failure
// has always been answerable.
func (r *Recorder) Record(s domain.APIRequestSample) {
	if r.coll != nil {
		r.coll.RecordAPIRequest(s.Method, s.Route, s.APIKeyID, s.Status, s.Latency)
	}
	if r.repo == nil || s.CompanyID == "" || s.APIKeyID == "" {
		return
	}
	at := s.At
	if at.IsZero() {
		at = r.now()
	}
	ms := int(s.Latency.Milliseconds())

	key := bucketKey{
		companyID:   s.CompanyID,
		apiKeyID:    s.APIKeyID,
		hour:        at.UTC().Truncate(time.Hour),
		route:       s.Route,
		method:      s.Method,
		statusClass: s.StatusClass(),
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	agg := r.buckets[key]
	if agg == nil {
		agg = &bucketAgg{}
		r.buckets[key] = agg
	}
	agg.requests++
	agg.latSumMS += int64(ms)
	if ms > agg.latMaxMS {
		agg.latMaxMS = ms
	}

	if !s.Failed() {
		return
	}
	if len(r.errs) >= r.maxErrs {
		r.dropped++
		return
	}
	r.errs = append(r.errs, domain.APIRequestError{
		CompanyID: s.CompanyID,
		APIKeyID:  s.APIKeyID,
		RequestID: s.RequestID,
		Method:    s.Method,
		Route:     s.Route,
		Status:    s.Status,
		ErrorCode: s.ErrorCode,
		ErrorType: s.ErrorType,
		LatencyMS: ms,
		CreatedAt: at,
	})
}

// Buffered reports how many buckets and failures are waiting. Tests assert on
// it; nothing in production reads it.
func (r *Recorder) Buffered() (buckets, errs int, dropped int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets), len(r.errs), r.dropped
}

// Flush writes everything buffered.
//
// The buffer is detached under the lock before any I/O, so requests continue to
// be recorded into a fresh map while this writes. A failed write logs and drops
// its batch: retrying would mean holding rows across an outage of unknown
// length, and an unbounded in-memory queue is how an observability feature
// takes down the thing it observes.
func (r *Recorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	buckets, errs, dropped := r.buckets, r.errs, r.dropped
	r.buckets = map[bucketKey]*bucketAgg{}
	r.errs = nil
	r.dropped = 0
	r.mu.Unlock()

	if dropped > 0 {
		// Loud, because the number is a statement about this tenant's traffic:
		// a thousand failures inside one flush interval is a script in a retry
		// storm, and the tab will be showing an incomplete list.
		logrus.WithField("dropped", dropped).
			Warn("api request error buffer overflowed; some /v1 failures were not recorded")
	}
	if r.repo == nil || (len(buckets) == 0 && len(errs) == 0) {
		return nil
	}

	rows := make([]domain.APIRequestStatRow, 0, len(buckets))
	for k, agg := range buckets {
		rows = append(rows, domain.APIRequestStatRow{
			CompanyID:    k.companyID,
			APIKeyID:     k.apiKeyID,
			BucketHour:   k.hour,
			Route:        k.route,
			Method:       k.method,
			StatusClass:  k.statusClass,
			Requests:     agg.requests,
			LatencyMSSum: agg.latSumMS,
			LatencyMSMax: agg.latMaxMS,
		})
	}

	var firstErr error
	if err := r.repo.UpsertStats(ctx, rows); err != nil {
		logrus.WithError(err).Warn("api request stats flush failed; this interval's counters are lost")
		firstErr = err
	}
	if err := r.repo.InsertErrors(ctx, errs); err != nil {
		logrus.WithError(err).Warn("api request error flush failed; this interval's failures are lost")
		if firstErr == nil {
			firstErr = err
		}
	}
	r.pruneIfDue(ctx)
	return firstErr
}

// pruneIfDue enforces retention at most once an hour per process.
func (r *Recorder) pruneIfDue(ctx context.Context) {
	now := r.now()
	r.mu.Lock()
	if !r.lastPrune.IsZero() && now.Sub(r.lastPrune) < pruneEvery {
		r.mu.Unlock()
		return
	}
	r.lastPrune = now
	r.mu.Unlock()

	n, err := r.repo.Prune(ctx, now.Add(-r.retain).UTC())
	if err != nil {
		logrus.WithError(err).Debug("api request observability prune failed")
		return
	}
	if n > 0 {
		logrus.WithField("rows", n).Debug("pruned expired api request observability")
	}
}

// Run flushes on an interval until ctx is cancelled, then flushes once more.
//
// The final flush gets a context of its own: ctx is already cancelled by the
// time we get here, and passing it would guarantee that the last interval of
// records — the ones covering whatever happened immediately before a shutdown,
// which is exactly when someone is looking — never reach the database.
func (r *Recorder) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			// The error is already logged inside Flush, with the failing half
			// named. There is nothing a loop can do about it that Flush has not
			// already decided.
			_ = r.Flush(ctx)
		case <-ctx.Done():
			r.Close()
			return
		}
	}
}

// Close flushes synchronously on a short deadline of its own. Called by the
// shutdown path, and by Run when its context ends.
func (r *Recorder) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.Flush(ctx)
}
