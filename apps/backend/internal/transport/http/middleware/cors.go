package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS returns a permissive CORS middleware suitable for local dev with the
// Vite frontend. In production tighten origins to the dashboard host.
//
// skipPrefixes names path prefixes that must never receive a CORS header.
// `/v1` is one (T-13): it authenticates with an API key, and a credential a
// browser can send is a credential that shipped in someone's bundle. With
// CORS_ORIGINS unset this middleware echoes *any* Origin, so leaving the
// public API inside it would have made a key usable from a web page — which
// is how the browser path and the machine path get conflated, and the browser
// path is deliberately a different credential (T-19's embed key).
//
// The check is a prefix rather than a group registration because this
// middleware is installed on the engine, above every group: a group-level
// install would have to be repeated for health, webhooks and the Metabase
// proxy, and the one somebody forgets is the one that regresses.
func CORS(allowOrigins []string, skipPrefixes ...string) gin.HandlerFunc {
	allowed := map[string]struct{}{}
	for _, o := range allowOrigins {
		allowed[o] = struct{}{}
	}
	return func(c *gin.Context) {
		for _, p := range skipPrefixes {
			if strings.HasPrefix(c.Request.URL.Path, p) {
				c.Next()
				return
			}
		}
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok || len(allowed) == 0 {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers",
			"Authorization, Content-Type, X-Requested-With, X-Twilio-Signature, X-Hub-Signature-256")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
