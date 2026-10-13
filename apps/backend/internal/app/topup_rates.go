package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
)

// The rupiah price of a dollar of credit (RIZ-83 R22).
//
// A fixed rate either loses money when the rupiah weakens or overcharges when it
// strengthens, so a top-up is priced from the market: the ECB's daily USD→IDR
// reference rate, served free and keyless by Frankfurter, plus a spread that is
// the one visible profit knob (TOPUP_FX_SPREAD_PCT). Three rules hold the rest
// together:
//
//  1. **No floats, anywhere a value is kept.** The source's decimal is parsed as
//     an exact rational and stored as rupiah × 10 000 (`_e4`); the spread is
//     basis points; the credit is integer division. 16 523,4 is 165234000, and
//     the same request priced twice is priced identically.
//  2. **Creating a top-up never waits on a free third party.** One fetch at a
//     time per process, bounded by TOPUP_FX_TIMEOUT_MS; a cached rate is used
//     for TOPUP_FX_CACHE_HOURS with no call at all, and for up to
//     TOPUP_FX_MAX_AGE_HOURS when a fetch fails — which covers ECB weekends and
//     a short outage.
//  3. **The fallback never undersells.** Past that, TOPUP_IDR_PER_USD (18 000
//     by default, above market plus 5%) is the effective rate, with no spread
//     on top. Every fallback logs `topup fx fallback` with its reason and
//     counts argentum_topup_fx_fallback_total, because a fallback that quietly
//     became the only rate would otherwise be found at reconciliation.

// The reasons a market rate was not usable — the label on
// argentum_topup_fx_fallback_total and the `reason` field of the warn line. A
// closed vocabulary, so the metric's label set is bounded.
const (
	FXReasonTimeout    = "timeout"
	FXReasonHTTPStatus = "http_status"
	FXReasonBadBody    = "bad_body"
	FXReasonOutOfRange = "out_of_range"
	// FXReasonStale is a fallback with a cached rate that was too old to use:
	// the fetch failed (its own reason is logged beside it) and the last good
	// rate is past TOPUP_FX_MAX_AGE_HOURS.
	FXReasonStale = "stale"
)

// The plausible band for a USD→IDR rate. A body outside it is treated as a
// broken source rather than as a market move: a rupiah at 900 or 60 000 to the
// dollar is a parsing bug or a changed API, and pricing credit off it would
// give it away or charge a hundredfold.
const (
	minPlausibleIDRPerUSDE4 = 5_000 * 10_000
	maxPlausibleIDRPerUSDE4 = 50_000 * 10_000
)

// fxBodyLimit caps what is read from the source. Frankfurter's answer is about
// 80 bytes; anything near this is not that answer.
const fxBodyLimit = 64 << 10

// FXQuote is one market rate as the source published it.
type FXQuote struct {
	// MarketIDRPerUSDE4 is rupiah per dollar × 10 000.
	MarketIDRPerUSDE4 int64 `json:"market_e4"`
	// RateDate is the ECB reference date, "2026-10-12" — not the fetch day.
	RateDate  string    `json:"rate_date"`
	FetchedAt time.Time `json:"fetched_at"`
}

// FXFetchError is a failed fetch with its reason.
type FXFetchError struct {
	Reason string
	Err    error
}

func (e *FXFetchError) Error() string {
	if e.Err == nil {
		return "fx fetch failed: " + e.Reason
	}
	return fmt.Sprintf("fx fetch failed (%s): %v", e.Reason, e.Err)
}

func (e *FXFetchError) Unwrap() error { return e.Err }

// fxReason is the reason an error carries, or http_status for a transport
// failure that is not a timeout — a refused connection or a DNS miss is an
// exchange that produced no 200, which is what that reason means.
func fxReason(err error) string {
	var fe *FXFetchError
	if errors.As(err, &fe) {
		return fe.Reason
	}
	if isTimeout(err) {
		return FXReasonTimeout
	}
	return FXReasonHTTPStatus
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// FXRateSource is where a market rate comes from. FrankfurterSource is the
// only real one; tests stub it, so no test in CI makes an outbound FX call.
type FXRateSource interface {
	FetchUSDIDR(ctx context.Context) (FXQuote, error)
}

// FrankfurterSource reads the ECB reference rate from Frankfurter. The URL
// carries no key — the service has none — and the response body is never
// logged.
type FrankfurterSource struct {
	url    string
	client *http.Client
}

// NewFrankfurterSource builds the client. timeout <= 0 falls back to 3s.
func NewFrankfurterSource(url string, timeout time.Duration) *FrankfurterSource {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &FrankfurterSource{url: url, client: &http.Client{Timeout: timeout}}
}

// FetchUSDIDR makes one request and classifies any failure.
func (f *FrankfurterSource) FetchUSDIDR(ctx context.Context) (FXQuote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return FXQuote{}, &FXFetchError{Reason: FXReasonHTTPStatus, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		if isTimeout(err) {
			return FXQuote{}, &FXFetchError{Reason: FXReasonTimeout, Err: err}
		}
		return FXQuote{}, &FXFetchError{Reason: FXReasonHTTPStatus, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return FXQuote{}, &FXFetchError{Reason: FXReasonHTTPStatus, Err: fmt.Errorf("status %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, fxBodyLimit))
	if err != nil {
		if isTimeout(err) {
			return FXQuote{}, &FXFetchError{Reason: FXReasonTimeout, Err: err}
		}
		return FXQuote{}, &FXFetchError{Reason: FXReasonBadBody, Err: err}
	}
	return ParseFrankfurterBody(raw)
}

// ParseFrankfurterBody reads `{"date":"…","rates":{"IDR":16523.4}}`.
//
// The number is decoded as a json.Number — its text — and parsed as an exact
// rational, so 16523.4 is 165234000 and never 165233999.99…; digits past the
// fourth decimal are truncated. A missing IDR is bad_body, and a value outside
// the plausible band is out_of_range.
func ParseFrankfurterBody(raw []byte) (FXQuote, error) {
	var body struct {
		Date  string                 `json:"date"`
		Rates map[string]json.Number `json:"rates"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return FXQuote{}, &FXFetchError{Reason: FXReasonBadBody, Err: err}
	}
	num, ok := body.Rates["IDR"]
	if !ok || num == "" {
		return FXQuote{}, &FXFetchError{Reason: FXReasonBadBody, Err: errors.New("no IDR rate in the response")}
	}
	e4, err := DecimalToE4(string(num))
	if err != nil {
		return FXQuote{}, &FXFetchError{Reason: FXReasonBadBody, Err: err}
	}
	if e4 < minPlausibleIDRPerUSDE4 || e4 > maxPlausibleIDRPerUSDE4 {
		return FXQuote{}, &FXFetchError{Reason: FXReasonOutOfRange, Err: fmt.Errorf("IDR rate %s is outside 5000–50000", num)}
	}
	q := FXQuote{MarketIDRPerUSDE4: e4}
	// A date that is not a date is dropped rather than refused: it is the
	// operator's context, not the price, and it is written to a DATE column
	// that would refuse the whole request over it.
	if _, err := time.Parse("2006-01-02", body.Date); err == nil {
		q.RateDate = body.Date
	}
	return q, nil
}

// DecimalToE4 parses a decimal string — "16523.4", "16523.4012",
// "1.65234e4" — into an integer × 10 000, truncating past the fourth decimal.
// big.Rat holds the value exactly; at no point is it a float.
func DecimalToE4(s string) (int64, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	scaled := new(big.Rat).Mul(r, big.NewRat(10_000, 1))
	out := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	if !out.IsInt64() {
		return 0, fmt.Errorf("%q is out of range", s)
	}
	return out.Int64(), nil
}

// EffectiveIDRPerUSDE4 is the market rate with the spread on top, rounded up:
// ⌈market × (10 000 + spread_bp) ÷ 10 000⌉. Up, because the spread is a margin
// and rounding it away would sell a fraction of a rupiah's worth of credit below
// it on every request.
func EffectiveIDRPerUSDE4(marketE4 int64, spreadBP int) int64 {
	num := marketE4 * int64(10_000+spreadBP)
	return (num + 9_999) / 10_000
}

// TopupCreditMicroUSD is the credit a rupiah amount buys at an effective rate:
// ⌊amount × 10¹⁰ ÷ effective_e4⌋ micro-USD. 10¹⁰ is 10⁶ (micro-USD per
// dollar) × 10⁴ (the rate's scale). Rounded down: a fraction of a micro-dollar
// is not worth a customer's or an auditor's question. TOPUP_MAX_IDR is capped
// in config so the product stays inside int64.
func TopupCreditMicroUSD(amountIDR, effectiveE4 int64) int64 {
	if effectiveE4 <= 0 {
		return 0
	}
	return amountIDR * 10_000_000_000 / effectiveE4
}

// TopupRatePolicy is the operator's half of pricing, from config.
type TopupRatePolicy struct {
	SpreadBP          int
	FallbackIDRPerUSD int64
	Timeout           time.Duration
	CacheFor          time.Duration
	MaxAge            time.Duration
}

// TopupRate is what a request created now would freeze.
type TopupRate struct {
	FX domain.TopupFX
	// FallbackReason is set when FX.Source is fallback.
	FallbackReason string
}

// FXRateCache keeps the last good market rate. Redis when the deployment has
// it, so every API replica prices from the same quote and one fetch a day
// serves all of them; in memory otherwise.
type FXRateCache interface {
	Load(ctx context.Context) (FXQuote, bool)
	Store(ctx context.Context, q FXQuote)
}

// fxCacheKey is the one Redis key, named in the PRD so an operator can flush
// it by hand to force a refetch.
const fxCacheKey = "topup:fx:usd_idr"

type redisFXCache struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewRedisFXCache returns nil for a nil client; TopupRates then keeps the
// quote in memory.
func NewRedisFXCache(rdb *redis.Client, ttl time.Duration) FXRateCache {
	if rdb == nil {
		return nil
	}
	return &redisFXCache{rdb: rdb, ttl: ttl}
}

func (c *redisFXCache) Load(ctx context.Context) (FXQuote, bool) {
	raw, err := c.rdb.Get(ctx, fxCacheKey).Bytes()
	if err != nil {
		return FXQuote{}, false
	}
	var q FXQuote
	if err := json.Unmarshal(raw, &q); err != nil || q.MarketIDRPerUSDE4 <= 0 {
		return FXQuote{}, false
	}
	return q, true
}

// Store keeps the quote for MaxAge, not CacheFor: a quote past its cache
// window is still the fallback-of-first-resort when a fetch fails, and Redis
// expiring it at 24 h would throw that away.
func (c *redisFXCache) Store(ctx context.Context, q FXQuote) {
	raw, err := json.Marshal(q)
	if err != nil {
		return
	}
	if err := c.rdb.Set(ctx, fxCacheKey, raw, c.ttl).Err(); err != nil {
		logrus.WithError(err).Debug("topup fx cache write failed; the next request refetches")
	}
}

// MemoryFXCache is the in-process FXRateCache.
type MemoryFXCache struct {
	mu sync.Mutex
	q  *FXQuote
}

func (c *MemoryFXCache) Load(context.Context) (FXQuote, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.q == nil {
		return FXQuote{}, false
	}
	return *c.q, true
}

func (c *MemoryFXCache) Store(_ context.Context, q FXQuote) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.q = &q
}

// TopupRates answers "what does a dollar of credit cost in rupiah right now".
type TopupRates struct {
	source FXRateSource
	cache  FXRateCache
	policy TopupRatePolicy
	now    func() time.Time

	// One fetch in flight per process; concurrent callers wait for it. Hand-
	// rolled singleflight, as in internal/dashboard/panelcache.go.
	mu       sync.Mutex
	inflight *fxCall
}

type fxCall struct {
	done  chan struct{}
	quote FXQuote
	err   error
}

// NewTopupRates wires it. A nil cache keeps the quote in memory; bad policy
// values are normalized rather than refused.
func NewTopupRates(source FXRateSource, cache FXRateCache, policy TopupRatePolicy) *TopupRates {
	if cache == nil {
		cache = &MemoryFXCache{}
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 3 * time.Second
	}
	if policy.CacheFor <= 0 {
		policy.CacheFor = 24 * time.Hour
	}
	if policy.MaxAge < policy.CacheFor {
		policy.MaxAge = policy.CacheFor
	}
	return &TopupRates{source: source, cache: cache, policy: policy, now: time.Now}
}

// WithClock replaces time.Now, for tests that age a cached quote.
func (r *TopupRates) WithClock(now func() time.Time) *TopupRates {
	if now != nil {
		r.now = now
	}
	return r
}

// Current returns the rate a request created now would freeze. It never fails:
// the worst case is the fallback, priced conservatively.
func (r *TopupRates) Current(ctx context.Context) TopupRate {
	cached, haveCache := r.cache.Load(ctx)
	if haveCache && r.now().Sub(cached.FetchedAt) < r.policy.CacheFor {
		return r.market(cached)
	}

	q, err := r.fetch(ctx)
	if err == nil {
		return r.market(q)
	}

	// The fetch failed. A quote younger than the max age is still the market
	// — ECB publishes nothing on a weekend, and a short outage is not a reason
	// to price at the fallback.
	cached, haveCache = r.cache.Load(ctx)
	age := time.Duration(0)
	if haveCache {
		age = r.now().Sub(cached.FetchedAt)
		if age < r.policy.MaxAge {
			logrus.WithError(err).WithFields(logrus.Fields{
				"reason":           fxReason(err),
				"cached_age_hours": int(age.Hours()),
			}).Warn("topup fx fetch failed; using the cached market rate")
			return r.market(cached)
		}
	}

	reason := fxReason(err)
	fields := logrus.Fields{"fallback_idr_per_usd": r.policy.FallbackIDRPerUSD}
	if haveCache {
		fields["fetch_reason"] = reason
		fields["cached_age_hours"] = int(age.Hours())
		reason = FXReasonStale
	}
	fields["reason"] = reason
	logrus.WithError(err).WithFields(fields).Warn("topup fx fallback")
	metrics.TopupFXFallback(reason)
	return TopupRate{
		FX: domain.TopupFX{
			Source:               domain.TopupFXSourceFallback,
			EffectiveIDRPerUSDE4: r.policy.FallbackIDRPerUSD * 10_000,
		},
		FallbackReason: reason,
	}
}

func (r *TopupRates) market(q FXQuote) TopupRate {
	m := q.MarketIDRPerUSDE4
	spread := r.policy.SpreadBP
	return TopupRate{FX: domain.TopupFX{
		Source:               domain.TopupFXSourceMarket,
		MarketIDRPerUSDE4:    &m,
		RateDate:             q.RateDate,
		SpreadBP:             &spread,
		EffectiveIDRPerUSDE4: EffectiveIDRPerUSDE4(m, spread),
	}}
}

// fetch collapses concurrent fetches into one. The call runs on a context
// detached from the first caller's, bounded by the timeout alone: if the
// caller that started it hung up, the others waiting on it should not fail
// for that.
func (r *TopupRates) fetch(ctx context.Context) (FXQuote, error) {
	r.mu.Lock()
	if call := r.inflight; call != nil {
		r.mu.Unlock()
		<-call.done
		return call.quote, call.err
	}
	// Re-read under the lock: a fetch that finished between this caller's
	// cache read and here has already stored a fresh quote, and starting a
	// second call for it would be the duplicate this lock exists to prevent.
	if q, ok := r.cache.Load(ctx); ok && r.now().Sub(q.FetchedAt) < r.policy.CacheFor {
		r.mu.Unlock()
		return q, nil
	}
	call := &fxCall{done: make(chan struct{})}
	r.inflight = call
	r.mu.Unlock()

	if r.source == nil {
		call.err = &FXFetchError{Reason: FXReasonHTTPStatus, Err: errors.New("no FX source configured")}
	} else {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.policy.Timeout)
		call.quote, call.err = r.source.FetchUSDIDR(fctx)
		cancel()
	}
	if call.err == nil {
		call.quote.FetchedAt = r.now()
		r.cache.Store(context.WithoutCancel(ctx), call.quote)
	}

	r.mu.Lock()
	r.inflight = nil
	r.mu.Unlock()
	close(call.done)
	return call.quote, call.err
}
