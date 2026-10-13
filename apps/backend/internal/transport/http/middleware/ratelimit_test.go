package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// limiterRouter runs the real Lua script against miniredis. The arithmetic
// that produces `RateLimit-Remaining` and `RateLimit-Reset` lives in that
// script, so a fake limiter would test nothing that ships.
func limiterRouter(t *testing.T, capacity int, refillPerSec float64) *gin.Engine {
	t.Helper()
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	limiter := NewRateLimiter(rdb, capacity, refillPerSec)
	if limiter == nil {
		t.Fatal("NewRateLimiter returned nil with a live client")
	}

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set(CtxAPIKeyID, "key-1") })
	v1.Use(limiter.APIKeyMiddleware())
	v1.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func getMe(r *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/me", nil))
	return w
}

// The headers are on allowed responses too. A client that only learns its
// budget from a 429 has already been refused once.
func TestRateLimitHeadersAreOnASuccessfulResponse(t *testing.T) {
	w := getMe(limiterRouter(t, 5, 1))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("RateLimit-Limit"); got != "5" {
		t.Errorf("RateLimit-Limit = %q, want 5", got)
	}
	if got := w.Header().Get("RateLimit-Remaining"); got != "4" {
		t.Errorf("RateLimit-Remaining = %q, want 4 after one of five", got)
	}
	if got := w.Header().Get("RateLimit-Reset"); got != "0" {
		t.Errorf("RateLimit-Reset = %q, want 0 while tokens remain", got)
	}
}

func TestARefusalCarriesRetryAfterAndTheEnvelope(t *testing.T) {
	// One token, and a refill slow enough that the reset is a number worth
	// reading rather than always 1.
	r := limiterRouter(t, 1, 0.2)

	if w := getMe(r); w.Code != http.StatusOK {
		t.Fatalf("first call = %d, want 200", w.Code)
	}
	w := getMe(r)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second call = %d, want 429", w.Code)
	}
	if got := envelopeOf(t, w).Code; got != "rate_limit_exceeded" {
		t.Errorf("code = %q, want rate_limit_exceeded", got)
	}
	for _, h := range []string{"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset", "Retry-After"} {
		if w.Header().Get(h) == "" {
			t.Errorf("no %s on a 429", h)
		}
	}
	// Retry-After is the bucket's own answer, not a flat 1: every refused
	// client retrying after the same second is a synchronised herd.
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", w.Header().Get("Retry-After"))
	}
	if retry != 5 {
		t.Errorf("Retry-After = %d, want 5 — one token at 0.2/s", retry)
	}
}

// RIZ-79 R10 / AC6: five sign-ups per address, the sixth refused with the
// sentence the signup page prints, and a second address unaffected.
func TestSignupIsLimitedPerAddress(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := NewRateLimiter(rdb, 5, 5.0/3600)

	r := gin.New()
	r.POST("/auth/signup", limiter.SignupMiddleware(), func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})
	signupFrom := func(addr string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
		req.RemoteAddr = addr + ":40000"
		r.ServeHTTP(w, req)
		return w
	}

	for i := 1; i <= 5; i++ {
		if w := signupFrom("203.0.113.7"); w.Code != http.StatusCreated {
			t.Fatalf("signup %d = %d, want 201", i, w.Code)
		}
	}
	w := signupFrom("203.0.113.7")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("signup 6 = %d, want 429", w.Code)
	}
	if got, want := w.Body.String(), `{"error":"Too many sign-ups from this network. Please try again in an hour."}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
	// One token back takes 720 s at five an hour. The header says so, rather
	// than inviting a retry in a second that will also be refused. A range,
	// not 720 exactly: the bucket refills by the whole second, so a run that
	// crosses a second boundary has already earned a sliver of a token.
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retry < 700 || retry > 720 {
		t.Errorf("Retry-After = %q, want about 720", w.Header().Get("Retry-After"))
	}

	if w := signupFrom("198.51.100.20"); w.Code != http.StatusCreated {
		t.Errorf("a second address = %d, want 201 — the bucket is per address", w.Code)
	}
}

// The signup bucket is its own namespace: an address that has exhausted the
// share limiter's bucket can still sign up, and the reverse.
func TestSignupBucketIsNotShared(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := NewRateLimiter(rdb, 1, 1.0/3600)

	r := gin.New()
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.POST("/auth/signup", limiter.SignupMiddleware(), ok)
	r.GET("/share/x", limiter.ShareMiddleware(), ok)
	hit := func(method, path string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "203.0.113.7:40000"
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := hit(http.MethodGet, "/share/x"); code != http.StatusOK {
		t.Fatalf("first share = %d", code)
	}
	if code := hit(http.MethodGet, "/share/x"); code != http.StatusTooManyRequests {
		t.Fatalf("second share = %d, want 429", code)
	}
	if code := hit(http.MethodPost, "/auth/signup"); code != http.StatusOK {
		t.Errorf("signup after the share bucket emptied = %d, want 200", code)
	}
}

// Redis down must not close the API. The limiter has always failed open; this
// pins that the headers being added did not change it.
func TestAnUnreachableRedisFailsOpen(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := NewRateLimiter(rdb, 1, 1)
	srv.Close()

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set(CtxAPIKeyID, "key-1") })
	v1.Use(limiter.APIKeyMiddleware())
	v1.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	if w := getMe(r); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — the limiter must fail open", w.Code)
	}
}
