package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// RequestLogging logs each handled request as a single structured log line
// with company_id, user_id, latency, status, and the path. Apply before
// other middleware so the log lines for protected endpoints carry tenant
// info.
func RequestLogging() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)

		fields := logrus.Fields{
			"method":     c.Request.Method,
			"path":       loggablePath(c),
			"status":     c.Writer.Status(),
			"latency_ms": latency.Milliseconds(),
			"client_ip":  c.ClientIP(),
		}
		if v, ok := c.Get("company_id"); ok {
			if s, _ := v.(string); s != "" {
				fields["company_id"] = s
			}
		}
		if v, ok := c.Get("user_id"); ok {
			if s, _ := v.(string); s != "" {
				fields["user_id"] = s
			}
		}
		// Read after c.Next(): RequestID (T-A1) is installed on the /v1 group,
		// below this engine-level middleware, so the id does not exist yet on
		// the way in. It is the field that makes a support request id resolve
		// to the log lines it produced.
		if s := c.GetString(CtxRequestID); s != "" {
			fields["request_id"] = s
		}
		if errs := c.Errors.String(); errs != "" {
			fields["errors"] = errs
		}

		entry := logrus.WithFields(fields)
		switch {
		case c.Writer.Status() >= 500:
			entry.Error("request")
		case c.Writer.Status() >= 400:
			entry.Warn("request")
		default:
			entry.Info("request")
		}
	}
}

// loggablePath is the request path with any credential in it removed.
//
// Every other route in this system carries its credential in a header, so the
// path has always been safe to log. `GET /share/:token` (T-V4) is the first
// where the path **is** the credential: the T-V4 gate found the token written
// to `api.log` in full on every page view, three times over, which turns read
// access to a log file into the ability to replay a link that was shared with
// somebody else.
//
// The route template rather than the value, so the line still says which route
// was hit and how long it took. Falling back to `c.FullPath()` in general
// would be the tidier rule and is deliberately not taken: for every other
// route the concrete path is what an operator greps for, and losing the ids in
// it would cost more than it buys.
func loggablePath(c *gin.Context) string {
	if c.FullPath() == "/share/:token" {
		return "/share/:token"
	}
	return c.Request.URL.Path
}
