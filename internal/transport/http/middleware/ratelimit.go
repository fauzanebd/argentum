package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
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
// refillPerSec is the steady-state rate.
func NewRateLimiter(rdb *redis.Client, capacity int, refillPerSec float64) *RateLimiter {
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
	script := redis.NewScript(tokenBucketLua)
	return func(c *gin.Context) {
		v, ok := c.Get("company_id")
		if !ok {
			c.Next()
			return
		}
		companyID, _ := v.(string)
		if companyID == "" {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
		defer cancel()
		key := fmt.Sprintf("rl:company:%s", companyID)

		res, err := script.Run(ctx, r.rdb, []string{key},
			r.capacity, r.refillPerSec, time.Now().Unix(),
		).Int()
		if err != nil {
			// Fail-open: don't block traffic if Redis is unhealthy.
			c.Next()
			return
		}
		if res == 0 {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}
