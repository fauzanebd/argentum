package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/tracing"
)

// Tracing opens the span an HTTP request runs under, and puts it on the
// request context so everything below inherits it.
//
// This is the half of `T-17b` that was missing. That ticket made a queue
// payload carry `traceparent` so a turn's two processes would land in one
// trace, and it works — `Inject` reads the current span from the context on
// the way in and `Extract` restores it in the worker. What nothing supplied
// was the span: `cmd/api` called `tracing.Init` and never started one, so
// `Inject` had an empty context to read, returned nil, and every worker turn
// began its own root trace. The 2026-08-09 gate ran a real turn against a live
// Jaeger with both processes exporting and found `argentum-api` absent from
// the service list entirely.
//
// Deliberately not otelgin. The dependency is already in the tree indirectly,
// but a server span is fifteen lines and this one names the route the way this
// codebase names things and stamps the request id an integrator actually
// quotes — see `tracing.Status`.
//
// Cheap when tracing is off: with no provider installed the tracer is a no-op
// and `Start` returns a non-recording span, which is the same guarantee every
// other seam in `internal/tracing` is built on.
func Tracing() gin.HandlerFunc {
	return func(c *gin.Context) {
		// FullPath is the route template and is empty for an unmatched
		// request. Read after Next()? No — the span has to exist before the
		// handler runs, and gin resolves the route before the chain starts.
		ctx, span := tracing.Request(c.Request.Context(), c.Request.Method, c.FullPath())
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		tracing.Status(span, c.Writer.Status(), c.GetString(CtxRequestID))
	}
}
