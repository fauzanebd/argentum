package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RIZ-79 QA B3: an idle bucket is kept until it would be full anyway. The key
// used to expire after 600 s whatever the rate, so an exhausted signup bucket
// came back full ten minutes later — five sign-ups every ten minutes, not five
// an hour.
func TestBucketTTLIsTheFullRefillTime(t *testing.T) {
	cases := []struct {
		name     string
		capacity int
		refill   float64
		want     int
	}{
		{name: "signup, 5 an hour", capacity: 5, refill: 5.0 / 3600, want: 3600},
		{name: "embed turns, 20 an hour", capacity: 20, refill: 20.0 / 3600, want: 3600},
		{name: "share, refills in a minute, keeps the old floor", capacity: 60, refill: 1, want: 600},
		{name: "API keys, 120 a minute", capacity: 120, refill: 2, want: 600},
		{name: "embed mint, 30 at 0.5/s", capacity: 30, refill: 0.5, want: 600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bucketTTL(tc.capacity, tc.refill); got != tc.want {
				t.Errorf("bucketTTL(%d, %g) = %d, want %d", tc.capacity, tc.refill, got, tc.want)
			}
		})
	}
}

func TestAnExhaustedSignupBucketOutlivesTenIdleMinutes(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	r := gin.New()
	r.POST("/auth/signup", NewRateLimiter(rdb, 5, 5.0/3600).SignupMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})
	signup := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
		req.RemoteAddr = "203.0.113.7:40000"
		r.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		signup()
	}
	if ttl := srv.TTL("rl:signup:203.0.113.7"); ttl < 3500*time.Second {
		t.Errorf("bucket TTL = %v, want about an hour", ttl)
	}

	// The QA reproduction: eleven idle minutes. miniredis only moves the key's
	// clock, so the refill arithmetic (which reads the API's clock) sees no
	// time pass — what is under test is that the key is still there.
	srv.FastForward(11 * time.Minute)
	if code := signup(); code != http.StatusTooManyRequests {
		t.Errorf("signup after 11 idle minutes = %d, want 429 — the bucket expired and came back full", code)
	}
}
