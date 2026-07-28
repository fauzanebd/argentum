package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// CtxRequestID is where RequestID leaves its value on the Gin context.
// apierr reads the same key to stamp every `/v1` error envelope, and it is
// declared here because this middleware is what fills it.
const CtxRequestID = "request_id"

// requestIDHeader is both what a caller may send and what every response
// carries back. One header in both directions: a client that correlates its
// own logs sends its id and gets it echoed; a client that sends nothing gets
// ours.
const requestIDHeader = "X-Request-Id"

// maxCallerRequestIDLen bounds a caller-supplied id. The value is logged and
// written to an audit row, so it is caller-controlled data heading for
// storage; 128 is longer than any sane trace id and short enough that it
// cannot be used to pad a log line into something else.
const maxCallerRequestIDLen = 128

// RequestID ensures every request has an id, on the response, in the log
// fields, and in the request context where the audit log (T-05) can reach it.
//
// **A caller's own id is accepted, but only after it is checked.** Echoing an
// arbitrary header into logs and into `agent_actions.request_id` is how a
// newline or a control character ends up in a log line that a human then
// reads as two events. Anything outside `[A-Za-z0-9_.:-]`, anything empty, or
// anything over 128 bytes is replaced by one of ours rather than rejected —
// the caller gets a working request and a usable id, and the id it sent is
// simply not the one that comes back.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitizeRequestID(c.GetHeader(requestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		c.Set(CtxRequestID, id)
		c.Header(requestIDHeader, id)
		c.Request = c.Request.WithContext(tenantctx.WithRequestID(c.Request.Context(), id))

		c.Next()
	}
}

// newRequestID mints `req_` + 16 random bytes. Random rather than sequential
// so an id in a support ticket says nothing about how much traffic the
// platform carries, and prefixed so it is recognisable when pasted alone.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a broken machine, not a condition to handle.
		// An empty id degrades correlation; it must not fail the request.
		return ""
	}
	return "req_" + hex.EncodeToString(b[:])
}

// sanitizeRequestID returns v when it is safe to echo and store, "" otherwise.
func sanitizeRequestID(v string) string {
	if v == "" || len(v) > maxCallerRequestIDLen {
		return ""
	}
	for i := 0; i < len(v); i++ {
		ch := v[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-', ch == '_', ch == '.', ch == ':':
		default:
			return ""
		}
	}
	return v
}
