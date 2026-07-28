package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// RateLimiter implements a per-company token bucket using Redis. The bucket
// refills at `refillPerSec` tokens per second up to `capacity`. Each request
// consumes one token. Implementation uses a simple atomic Lua script so it
// is correct under concurrent load.
type RateLimiter struct {
	rdb          *redis.Client
	capacity     int
	refillPerSec float64
}

// NewRateLimiter constructs a RateLimiter. capacity is the burst budget;
// refillPerSec is the steady-state rate. A nil client yields a nil limiter —
// callers already test the result for nil before installing it, and the
// alternative is a middleware that panics on the first authenticated request
// instead of simply not limiting.
func NewRateLimiter(rdb *redis.Client, capacity int, refillPerSec float64) *RateLimiter {
	if rdb == nil {
		return nil
	}
	if capacity <= 0 {
		capacity = 60
	}
	if refillPerSec <= 0 {
		refillPerSec = 1.0
	}
	return &RateLimiter{rdb: rdb, capacity: capacity, refillPerSec: refillPerSec}
}

// TokenBucketLua atomically refills the bucket and consumes one token.
// Returns 1 if allowed, 0 otherwise.
//
// KEYS[1] = bucket key
// ARGV[1] = capacity
// ARGV[2] = refill_per_sec
// ARGV[3] = now (unix seconds, integer)
const tokenBucketLua = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local data = redis.call('HMGET', key, 'tokens', 'updated')
local tokens = tonumber(data[1])
local updated = tonumber(data[2])
if tokens == nil then
  tokens = capacity
  updated = now
end

local delta = math.max(0, now - updated) * refill
tokens = math.min(capacity, tokens + delta)

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call('HMSET', key, 'tokens', tokens, 'updated', now)
redis.call('EXPIRE', key, 600)
return allowed
`

// Middleware returns a Gin handler that rate-limits per company_id (set on
// the gin context by the Auth middleware). Anonymous requests bypass the
// limiter — apply this middleware *after* Auth.
func (r *RateLimiter) Middleware() gin.HandlerFunc {
	return r.limitBy("rl:company:", "company_id", func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "rate limit exceeded",
		})
	})
}

// APIKeyMiddleware rate-limits per API key (T-13), in a bucket namespace of
// its own.
//
// A separate bucket rather than a separate limiter: the mechanism is already
// correct under concurrency and a second implementation is a second thing to
// get wrong. What must not be shared is the *bucket* — a company's staff
// using the dashboard and that company's nightly job hitting `/v1` are
// different traffic with different burst shapes, and letting one exhaust the
// other's budget makes an integration's reliability depend on how busy the
// office is.
//
// The refusal goes out in the `/v1` envelope, because everything under `/v1`
// does.
func (r *RateLimiter) APIKeyMiddleware() gin.HandlerFunc {
	return r.limitBy("rl:apikey:", CtxAPIKeyID, func(c *gin.Context) {
		apierr.Abort(c, apierr.TypeRateLimit, "rate_limit_exceeded",
			"Too many requests on this API key. Retry in a moment.")
	})
}

// limitBy is the shared body: read the bucket identity off the Gin context,
// consume a token, and hand the refusal to the caller's own writer so the two
// surfaces can answer in their own error formats.
func (r *RateLimiter) limitBy(prefix, ctxKey string, refuse func(*gin.Context)) gin.HandlerFunc {
	script := redis.NewScript(tokenBucketLua)
	return func(c *gin.Context) {
		v, ok := c.Get(ctxKey)
		if !ok {
			c.Next()
			return
		}
		id, _ := v.(string)
		if id == "" {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
		defer cancel()

		res, err := script.Run(ctx, r.rdb, []string{prefix + id},
			r.capacity, r.refillPerSec, time.Now().Unix(),
		).Int()
		if err != nil {
			// Fail-open: don't block traffic if Redis is unhealthy.
			c.Next()
			return
		}
		if res == 0 {
			c.Header("Retry-After", "1")
			refuse(c)
			return
		}
		c.Next()
	}
}
