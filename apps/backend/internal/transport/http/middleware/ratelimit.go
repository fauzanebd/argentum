package middleware

import (
	"context"
	"net/http"
	"strconv"
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
//
// It returns three values rather than one, because T-A1 requires
// `RateLimit-Remaining` and `RateLimit-Reset` on **every** response and not
// just on a refusal. Computing either outside the script would mean a second
// read of a bucket that has already moved.
//
//	[1] allowed  — 1 or 0
//	[2] tokens   — whole tokens left after this request
//	[3] reset    — seconds until at least one token is available, 0 if now
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

local reset = 0
if tokens < 1 then
  reset = math.ceil((1 - tokens) / refill)
end
return {allowed, math.floor(tokens), reset}
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

// EmbedMiddleware rate-limits per `(company_id, embed_user_ref)` (T-19), in a
// third bucket namespace.
//
// **The pair, not the ref alone.** An `embed_user_ref` is a string the tenant
// chose, so `emp_1` is a different person at every company that uses it —
// keying on it alone would let one tenant's traffic exhaust another's budget,
// and would let a tenant discover another tenant's usage by watching their own
// refusals.
//
// A third bucket rather than a third limiter, for APIKeyMiddleware's reason:
// the mechanism is already correct under concurrency, and what must not be
// shared is the bucket. A tenant's staff in the dashboard, their nightly job on
// `/v1`, and one visitor of their website are three traffic shapes with no
// reason to spend each other's tokens.
func (r *RateLimiter) EmbedMiddleware() gin.HandlerFunc {
	limit := r.limitBy("rl:embed:", ctxEmbedBucket, func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many messages. Give it a moment.",
		})
	})
	return func(c *gin.Context) {
		company := c.GetString("company_id")
		ref := EmbedUserRef(c)
		if company != "" && ref != "" {
			c.Set(ctxEmbedBucket, company+":"+ref)
		}
		limit(c)
	}
}

// EmbedMintMiddleware rate-limits the session mint by client address (T-19).
//
// The address is the weakest identity in the system and it is used because at
// this point in the request there is no other: the mint runs before any
// credential has been verified — that is what it is for. What it defends is
// signature guessing. `hmac.Equal` makes each attempt uninformative and 256
// bits makes the space unsearchable, so this is defence in depth rather than
// the control; what it actually buys is that an unauthenticated route which
// does a database read and an AES-GCM open per call cannot be driven at line
// rate by one host.
func (r *RateLimiter) EmbedMintMiddleware() gin.HandlerFunc {
	limit := r.limitBy("rl:embedmint:", ctxEmbedBucket, func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many session requests. Try again in a moment.",
		})
	})
	return func(c *gin.Context) {
		c.Set(ctxEmbedBucket, c.ClientIP())
		limit(c)
	}
}

// ctxEmbedBucket is where the two embed limiters leave the identity they limit
// by — the address before a session exists, the `(company, ref)` pair after.
// Unexported for ctxShareBucket's reason: nothing outside this file should be
// able to choose what a bucket is keyed on. The two never run on the same
// request, and their Redis namespaces differ, so sharing the context key
// cannot mix the buckets.
const ctxEmbedBucket = "embed_bucket"

// ctxShareBucket is where ShareMiddleware leaves the identity it limits by.
// Unexported and set by that middleware itself: nothing else should be able to
// choose what a public route's bucket is keyed on.
const ctxShareBucket = "share_bucket"

// ShareMiddleware rate-limits `GET /share/:token` by client address (T-V4).
//
// Neither of the other two identities exists here — there is no session and no
// key — so the bucket is the address, which is the weakest identity in the
// system and is used because it is the only one. What it is actually
// defending is token guessing: 256 bits is not brute-forceable, but a public
// route that runs a database read per request is worth bounding regardless,
// and an attacker distributed enough to beat a per-address bucket is not the
// one this is for.
//
// The refusal is a plain JSON body, not the `/v1` envelope. This route is not
// part of the published contract and its reader is a browser.
func (r *RateLimiter) ShareMiddleware() gin.HandlerFunc {
	limit := r.limitBy("rl:share:", ctxShareBucket, func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many requests. Try again in a moment.",
		})
	})
	return func(c *gin.Context) {
		c.Set(ctxShareBucket, c.ClientIP())
		limit(c)
	}
}

// limitBy is the shared body: read the bucket identity off the Gin context,
// consume a token, and hand the refusal to the caller's own writer so the two
// surfaces can answer in their own error formats.
//
// The `RateLimit-*` headers go out on allowed requests too, which is the
// whole point of them: a client that only learns its budget from a 429 has
// already been refused once. They are emitted on both surfaces rather than
// only on `/v1` — the dashboard reading its own remaining budget is
// harmless, and one code path is one thing to keep correct.
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
		).Int64Slice()
		if err != nil || len(res) != 3 {
			// Fail-open: don't block traffic if Redis is unhealthy.
			c.Next()
			return
		}
		allowed, remaining, reset := res[0], res[1], res[2]

		c.Header("RateLimit-Limit", strconv.Itoa(r.capacity))
		c.Header("RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		c.Header("RateLimit-Reset", strconv.FormatInt(reset, 10))

		if allowed == 0 {
			// Retry-After carries the bucket's own answer rather than a flat
			// 1s. Every refused client retrying after the same second is how
			// a rate limit turns into a synchronised thundering herd; floored
			// at 1 because a `Retry-After: 0` invites an immediate retry.
			if reset < 1 {
				reset = 1
			}
			c.Header("Retry-After", strconv.FormatInt(reset, 10))
			refuse(c)
			return
		}
		c.Next()
	}
}
