package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// Enabled is the `/v1` kill switch (T-A1, `API_V1_ENABLED`).
//
// It is installed **above** APIKeyAuth on purpose. A disabled public surface
// should not read a credential, spend a database round trip on it, or write a
// log line naming a key: it should answer and stop. It also means the switch
// covers `/v1/me`, which is the route an integrator polls when their calls
// start failing — a 503 there is the whole answer.
//
// 503 rather than 404: the routes exist and are coming back. A 404 tells an
// integrator they got the path wrong and sends them to re-read the docs.
func Enabled(on bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if on {
			c.Next()
			return
		}
		// Retry-After is a guess by definition — nobody knows when a kill
		// switch flips back — but a client with no value at all retries
		// immediately and in a loop. 30s is the smallest number that keeps a
		// disabled API from being hammered by its own consumers.
		c.Header("Retry-After", "30")
		apierr.AbortStatus(c, http.StatusServiceUnavailable, apierr.TypeServer, "api_disabled",
			"The Argentum public API is temporarily unavailable. Retry shortly.", "")
	}
}

// MaxBodyBytes refuses a request body over the cap (T-A1,
// `API_V1_MAX_BODY_BYTES`).
//
// Two mechanisms, because one of them is a lie a caller can tell. The
// Content-Length check refuses before a byte is read, which is what protects
// the renderer from a 500 000-row spec; MaxBytesReader is what protects it
// from a chunked body that declares no length at all, and surfaces as a bind
// error inside the handler rather than as a status this middleware chooses.
//
// 413 with `type: invalid_request` — see apierr.AbortStatus for why the class
// is not widened to hold a status of its own.
func MaxBodyBytes(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if max <= 0 {
			c.Next()
			return
		}
		if c.Request.ContentLength > max {
			apierr.AbortStatus(c, http.StatusRequestEntityTooLarge, apierr.TypeInvalidRequest,
				"request_too_large",
				"That request body is larger than the "+strconv.FormatInt(max, 10)+" byte limit.", "")
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}
