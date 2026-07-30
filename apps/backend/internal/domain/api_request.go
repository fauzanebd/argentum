package domain

import (
	"context"
	"time"
)

// Integrator-facing observability over `/v1` (T-A5).
//
// The question these types answer belongs to the tenant, not to us: an
// integrator whose script is getting a 403 at 11pm should be able to see the
// 403. Everything here is company-scoped for that reason — the tab that reads
// it is the tenant's own, and a per-key error list is the one record that makes
// a machine credential debuggable by whoever deployed it.

// APIRequestSample is one finished `/v1` request, as the middleware observed
// it. It is the input to the recorder and is never stored in this shape: the
// counters go to a rollup and only the failures keep their detail.
//
// CompanyID and APIKeyID are empty when the request never authenticated. Such a
// sample still counts on /metrics and is never persisted, because a 401 from an
// unknown credential belongs to no tenant — attributing it to one would mean
// guessing, and showing it to all of them would leak another tenant's traffic.
type APIRequestSample struct {
	CompanyID string
	APIKeyID  string
	// RequestID is the value the caller was handed in `X-Request-Id`. It is the
	// only handle they have when they ask us what happened.
	RequestID string
	Method    string
	// Route is the gin pattern (`/v1/reports/:id`), not the concrete path. See
	// 032_api_observability.up.sql for why the distinction is load-bearing.
	Route  string
	Status int
	// ErrorCode and ErrorType are the envelope's own fields, empty on a 2xx and
	// on any failure that did not go through apierr.
	ErrorCode string
	ErrorType string
	Latency   time.Duration
	At        time.Time
}

// StatusClass is the first digit of the status: 2, 4 or 5. Anything outside
// 100–599 lands in 5, because a status we cannot classify is our problem.
func (s APIRequestSample) StatusClass() int {
	switch {
	case s.Status >= 200 && s.Status < 300:
		return 2
	case s.Status >= 400 && s.Status < 500:
		return 4
	case s.Status >= 300 && s.Status < 400:
		// /v1 issues no redirects. One arriving here is a wiring accident, and
		// counting it as a success would hide it.
		return 3
	default:
		return 5
	}
}

// Failed reports whether this sample belongs in the error list. Everything
// outside 2xx does, including the 3xx that should never happen.
func (s APIRequestSample) Failed() bool { return s.StatusClass() != 2 }

// APIRequestStatRow is one upsertable rollup bucket.
type APIRequestStatRow struct {
	CompanyID    string
	APIKeyID     string
	BucketHour   time.Time
	Route        string
	Method       string
	StatusClass  int
	Requests     int64
	LatencyMSSum int64
	LatencyMSMax int
}

// APIRequestError is one recorded failure, as the dashboard reads it back.
type APIRequestError struct {
	ID string `json:"id"`
	// CompanyID is `json:"-"` because every read of this type is already
	// company-scoped by the route that served it: repeating the tenant's own id
	// in each of fifty rows tells the reader nothing. It is on the struct
	// because the write needs it, and carrying it here beats a second row type
	// or a parallel slice that can fall out of step with the rows it labels.
	CompanyID string `json:"-"`
	APIKeyID  string `json:"api_key_id"`
	RequestID string `json:"request_id"`
	Method    string `json:"method"`
	Route     string `json:"route"`
	Status    int    `json:"status"`
	// ErrorCode is what an integrator matches on in their own code, so it is
	// the field the tab shows first. Empty means the failure never reached the
	// error envelope — a panic, or a handler writing its own body.
	ErrorCode string    `json:"error_code,omitempty"`
	ErrorType string    `json:"error_type,omitempty"`
	LatencyMS int       `json:"latency_ms"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKeyRequestStats is the per-key summary the dashboard shows beside a key.
//
// The window is on the struct rather than implied, because "412 requests" means
// nothing without it and the tab must not have to remember which window it
// asked for.
type APIKeyRequestStats struct {
	APIKeyID    string `json:"api_key_id"`
	WindowHours int    `json:"window_hours"`
	Requests    int64  `json:"requests"`
	Failed      int64  `json:"failed"`
	// ErrorRatePct is failed/requests as a percentage, rounded to one decimal.
	// Derived here rather than in the dashboard so the number in the tab and the
	// number in a support conversation are computed once.
	ErrorRatePct float64 `json:"error_rate_pct"`
	AvgLatencyMS int     `json:"avg_latency_ms"`
	MaxLatencyMS int     `json:"max_latency_ms"`
}

// APIRequestRepository persists and reads back `/v1` request observability.
//
// There is no per-request Create. Writes arrive in batches from
// internal/apiobs, which is what keeps a Postgres round trip off the request
// path of a machine API — and what makes the write volume a function of the
// flush interval rather than of the traffic.
type APIRequestRepository interface {
	// UpsertStats adds the batch's counters onto whatever is already stored for
	// each bucket. It must be additive: two API processes flush the same
	// (key, hour, route) bucket independently, and a last-write-wins UPDATE
	// would silently discard one of them.
	UpsertStats(ctx context.Context, rows []APIRequestStatRow) error
	InsertErrors(ctx context.Context, rows []APIRequestError) error
	// StatsByKey summarises every key that has traffic in the window, keyed by
	// key id. Keys with no traffic are absent rather than zero-valued: the
	// caller already holds the roster and can tell "no calls" from "no key".
	StatsByKey(ctx context.Context, companyID string, since time.Time) (map[string]*APIKeyRequestStats, error)
	// RecentErrors returns the newest failures first. keyID empty means every
	// key in the company.
	RecentErrors(ctx context.Context, companyID, keyID string, limit int) ([]*APIRequestError, error)
	// Prune drops rows older than before. Retention is enforced here rather
	// than by a migration or a cron: the process that writes the rows is the
	// one that knows it is still running.
	Prune(ctx context.Context, before time.Time) (int64, error)
}
