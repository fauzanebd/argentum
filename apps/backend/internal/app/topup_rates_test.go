package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
)

// RIZ-83 R22: the rupiah price of a dollar of credit. No test here makes an
// outbound call — every source is a stub or an httptest server.

// stubFX is a counting FXRateSource.
type stubFX struct {
	calls atomic.Int32
	quote FXQuote
	err   error
	delay time.Duration
}

func (s *stubFX) FetchUSDIDR(ctx context.Context) (FXQuote, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return FXQuote{}, &FXFetchError{Reason: FXReasonTimeout, Err: ctx.Err()}
		}
	}
	return s.quote, s.err
}

func marketQuote(idr int64) FXQuote {
	return FXQuote{MarketIDRPerUSDE4: idr * 10_000, RateDate: "2026-10-12"}
}

func ratePolicy() TopupRatePolicy {
	return TopupRatePolicy{
		SpreadBP:          500,
		FallbackIDRPerUSD: 18_000,
		Timeout:           200 * time.Millisecond,
		CacheFor:          24 * time.Hour,
		MaxAge:            72 * time.Hour,
	}
}

// Q28: the parse keeps every digit it is given, to the fourth decimal, and
// never goes through a float.
func TestDecimalToE4(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"16523.4", 165_234_000},
		{"16523.4012", 165_234_012},
		{"16523.40129", 165_234_012}, // truncated past four places
		{"1.65234e4", 165_234_000},
		{"16000", 160_000_000},
		{"0.1", 1_000},
	}
	for _, c := range cases {
		got, err := DecimalToE4(c.in)
		if err != nil || got != c.want {
			t.Errorf("DecimalToE4(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	if _, err := DecimalToE4("sixteen thousand"); err == nil {
		t.Error("a word parsed as a rate")
	}
}

// Q28: the effective rate and the credit, worked by hand.
//
//	165234000 × 10500 ÷ 10000 = 173495700 exactly
//	20000  × 10¹⁰ ÷ 173495700 = 1152766.3…  → 1152766
//	100000 × 10¹⁰ ÷ 173495700 = 5763831.6…  → 5763831
//	160000000 × 1.05 = 168000000; 150000 × 10¹⁰ ÷ that = 8928571.4… → 8928571
//	fallback 18000: 100000 × 10¹⁰ ÷ 180000000 = 5555555.5… → 5555555
func TestTopupPricing(t *testing.T) {
	if got := EffectiveIDRPerUSDE4(165_234_000, 500); got != 173_495_700 {
		t.Fatalf("effective at 16523.40 + 5%% = %d, want 173495700", got)
	}
	if got := EffectiveIDRPerUSDE4(160_000_000, 500); got != 168_000_000 {
		t.Fatalf("effective at 16000 + 5%% = %d, want 168000000", got)
	}
	// Rounded up: a spread rounded away is margin given away.
	if got := EffectiveIDRPerUSDE4(165_234_001, 500); got != 173_495_702 {
		t.Errorf("effective at 16523.4001 + 5%% = %d, want 173495702 (⌈173495701.05⌉)", got)
	}
	cases := []struct {
		amount, effective, want int64
	}{
		{20_000, 173_495_700, 1_152_766},
		{100_000, 173_495_700, 5_763_831},
		{150_000, 173_495_700, 8_645_747},
		{5_000_000, 173_495_700, 288_191_580},
		{150_000, 168_000_000, 8_928_571},
		{100_000, 180_000_000, 5_555_555},
		{150_000, 187_000_000, 8_021_390}, // AC23's new request: 17000 + 10%
		{100_000, 200_000_000, 5_000_000}, // Q9: 20000, no spread
	}
	for _, c := range cases {
		if got := TopupCreditMicroUSD(c.amount, c.effective); got != c.want {
			t.Errorf("credit for Rp %d at %d = %d, want %d", c.amount, c.effective, got, c.want)
		}
	}
	if got := TopupCreditMicroUSD(100_000, 0); got != 0 {
		t.Errorf("a zero rate priced a credit: %d", got)
	}
}

func TestParseFrankfurterBody(t *testing.T) {
	q, err := ParseFrankfurterBody([]byte(`{"amount":1.0,"base":"USD","date":"2026-10-12","rates":{"IDR":16523.4}}`))
	if err != nil || q.MarketIDRPerUSDE4 != 165_234_000 || q.RateDate != "2026-10-12" {
		t.Fatalf("good body: %+v, %v", q, err)
	}
	q, err = ParseFrankfurterBody([]byte(`{"date":"yesterday","rates":{"IDR":16000}}`))
	if err != nil || q.RateDate != "" {
		t.Errorf("a date that is not a date is dropped, not refused: %+v, %v", q, err)
	}
	for _, c := range []struct {
		body, reason string
	}{
		{`{"rates":{}}`, FXReasonBadBody},
		{`{"rates":{"EUR":0.9}}`, FXReasonBadBody},
		{`<html>busy</html>`, FXReasonBadBody},
		{`{"rates":{"IDR":"sixteen"}}`, FXReasonBadBody},
		{`{"rates":{"IDR":900}}`, FXReasonOutOfRange},
		{`{"rates":{"IDR":60000}}`, FXReasonOutOfRange},
		{`{"rates":{"IDR":-16000}}`, FXReasonOutOfRange},
	} {
		_, err := ParseFrankfurterBody([]byte(c.body))
		if got := fxReason(err); err == nil || got != c.reason {
			t.Errorf("%s: reason %q (%v), want %q", c.body, got, err, c.reason)
		}
	}
}

func TestFrankfurterSourceClassifiesTheExchange(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("from") != "USD" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"base":"USD","date":"2026-10-12","rates":{"IDR":16000}}`))
	}))
	defer srv.Close()
	src := NewFrankfurterSource(srv.URL+"/latest?from=USD&to=IDR", time.Second)

	q, err := src.FetchUSDIDR(context.Background())
	if err != nil || q.MarketIDRPerUSDE4 != 160_000_000 {
		t.Fatalf("200: %+v, %v", q, err)
	}
	status.Store(http.StatusInternalServerError)
	if _, err := src.FetchUSDIDR(context.Background()); fxReason(err) != FXReasonHTTPStatus {
		t.Errorf("500: %v, want http_status", err)
	}
}

// AC25 / Q29: a quote fetched two hours ago serves everything with no call.
func TestTopupRatesServesAFreshQuoteWithoutFetching(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	cache := &MemoryFXCache{}
	q := marketQuote(16_000)
	q.FetchedAt = now.Add(-2 * time.Hour)
	cache.Store(context.Background(), q)
	src := &stubFX{quote: marketQuote(17_000)}
	rates := NewTopupRates(src, cache, ratePolicy()).WithClock(func() time.Time { return now })

	for i := 0; i < 4; i++ { // three top-ups and the options load
		r := rates.Current(context.Background())
		if r.FX.Source != domain.TopupFXSourceMarket || *r.FX.MarketIDRPerUSDE4 != 160_000_000 {
			t.Fatalf("call %d froze %+v, want the cached 16000", i, r.FX)
		}
	}
	if n := src.calls.Load(); n != 0 {
		t.Errorf("the source was called %d times, want 0", n)
	}
}

// Q29: twenty concurrent callers with nothing cached share one fetch.
func TestTopupRatesCollapsesConcurrentFetches(t *testing.T) {
	src := &stubFX{quote: marketQuote(16_000), delay: 50 * time.Millisecond}
	rates := NewTopupRates(src, nil, ratePolicy())

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]TopupRate, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = rates.Current(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	if n := src.calls.Load(); n != 1 {
		t.Errorf("the source was called %d times, want 1", n)
	}
	for i, r := range results {
		if r.FX.Source != domain.TopupFXSourceMarket || r.FX.EffectiveIDRPerUSDE4 != 168_000_000 {
			t.Errorf("caller %d got %+v", i, r.FX)
		}
	}
}

// AC26 / Q30: a failing source falls back on a quote younger than the max
// age, and past it — or with none — on the configured rate, logged and
// counted with its reason.
func TestTopupRatesFallBack(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	failures := []struct {
		name   string
		err    error
		reason string
	}{
		{"500", &FXFetchError{Reason: FXReasonHTTPStatus, Err: errors.New("status 500")}, FXReasonHTTPStatus},
		{"no IDR", &FXFetchError{Reason: FXReasonBadBody}, FXReasonBadBody},
		{"IDR 900", &FXFetchError{Reason: FXReasonOutOfRange}, FXReasonOutOfRange},
		{"IDR 60000", &FXFetchError{Reason: FXReasonOutOfRange}, FXReasonOutOfRange},
	}
	for _, f := range failures {
		for _, c := range []struct {
			name       string
			cachedAge  time.Duration // 0 = nothing cached
			wantMarket bool
			wantReason string
		}{
			{"cached 30h ago", 30 * time.Hour, true, ""},
			{"cached 80h ago", 80 * time.Hour, false, FXReasonStale},
			{"nothing cached", 0, false, f.reason},
		} {
			t.Run(f.name+", "+c.name, func(t *testing.T) {
				hook := logrustest.NewGlobal()
				defer hook.Reset()
				cache := &MemoryFXCache{}
				if c.cachedAge > 0 {
					q := marketQuote(16_000)
					q.FetchedAt = now.Add(-c.cachedAge)
					cache.Store(context.Background(), q)
				}
				before := metrics.Default().GetSnapshot().Domain.TopupFXFallbacks[c.wantReason]
				rates := NewTopupRates(&stubFX{err: f.err}, cache, ratePolicy()).
					WithClock(func() time.Time { return now })

				r := rates.Current(context.Background())
				after := metrics.Default().GetSnapshot().Domain.TopupFXFallbacks[c.wantReason]

				if c.wantMarket {
					if r.FX.Source != domain.TopupFXSourceMarket || *r.FX.MarketIDRPerUSDE4 != 160_000_000 {
						t.Fatalf("froze %+v, want the cached market rate", r.FX)
					}
					if fallbackLines(hook) != 0 {
						t.Error("a usable cached rate logged a fallback")
					}
					return
				}
				if r.FX.Source != domain.TopupFXSourceFallback || r.FX.EffectiveIDRPerUSDE4 != 180_000_000 ||
					r.FX.MarketIDRPerUSDE4 != nil || r.FX.SpreadBP != nil {
					t.Fatalf("froze %+v, want fallback 180000000 with no market and no spread", r.FX)
				}
				if got := TopupCreditMicroUSD(100_000, r.FX.EffectiveIDRPerUSDE4); got != 5_555_555 {
					t.Errorf("Rp 100.000 at the fallback = %d, want 5555555", got)
				}
				if r.FallbackReason != c.wantReason {
					t.Errorf("reason %q, want %q", r.FallbackReason, c.wantReason)
				}
				if n := fallbackLines(hook); n != 1 {
					t.Errorf("%d `topup fx fallback` lines, want 1", n)
				}
				for _, e := range hook.AllEntries() {
					if e.Message == "topup fx fallback" && (e.Level != logrus.WarnLevel || e.Data["reason"] != c.wantReason) {
						t.Errorf("fallback line: level %v, fields %v", e.Level, e.Data)
					}
				}
				if after != before+1 {
					t.Errorf("argentum_topup_fx_fallback_total{reason=%q} went %d → %d, want +1", c.wantReason, before, after)
				}
			})
		}
	}
}

func fallbackLines(hook *logrustest.Hook) int {
	n := 0
	for _, e := range hook.AllEntries() {
		if e.Message == "topup fx fallback" {
			n++
		}
	}
	return n
}

// AC27 / Q31: a source that hangs costs the timeout and no more, through the
// real HTTP client.
func TestTopupRatesTimesOutAHangingSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer srv.Close()
	policy := ratePolicy()
	policy.Timeout = 150 * time.Millisecond
	rates := NewTopupRates(NewFrankfurterSource(srv.URL, policy.Timeout), nil, policy)

	start := time.Now()
	r := rates.Current(context.Background())
	took := time.Since(start)
	if took > policy.Timeout+time.Second {
		t.Errorf("took %v with a %v timeout", took, policy.Timeout)
	}
	if r.FX.Source != domain.TopupFXSourceFallback || r.FallbackReason != FXReasonTimeout {
		t.Errorf("froze %+v reason %q, want a fallback for timeout", r.FX, r.FallbackReason)
	}
}

// A failure is not remembered as a success: the next call tries again.
func TestTopupRatesRetriesAfterAFailedFetch(t *testing.T) {
	src := &stubFX{err: &FXFetchError{Reason: FXReasonHTTPStatus}}
	rates := NewTopupRates(src, nil, ratePolicy())
	if r := rates.Current(context.Background()); r.FX.Source != domain.TopupFXSourceFallback {
		t.Fatalf("first: %+v", r.FX)
	}
	src.err = nil
	src.quote = marketQuote(16_000)
	if r := rates.Current(context.Background()); r.FX.Source != domain.TopupFXSourceMarket {
		t.Fatalf("second: %+v", r.FX)
	}
	if n := src.calls.Load(); n != 2 {
		t.Errorf("%d calls, want 2", n)
	}
}
