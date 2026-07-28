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
			"path":       c.Request.URL.Path,
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
