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

// EmbedCORS is the CORS policy for `/api/embed` (T-19). The engine-level CORS
// above skips that prefix, because the two surfaces answer opposite questions:
// the dashboard's list is a fixed set of hosts we operate, and this one is
// "every site any tenant has allowlisted" — a set that changes whenever an
// admin edits a key, and that no deployment-time env var can hold.
//
// **It reflects the Origin, and that is safe here for two specific reasons:**
//
//  1. **No credentials.** `Access-Control-Allow-Credentials` is deliberately
//     absent, so a browser sends no cookie and carries no ambient authority to
//     this surface. Reflection without credentials grants a page nothing it
//     could not already do with a plain server-side request.
//  2. **The real check is behind it.** CORS decides who may *read a response*;
//     it is not an access control. What actually gates this surface is the
//     origin allowlist and the HMAC inside MintSession, and an origin nobody
//     allowlisted reads a 403 with no token in it.
//
// Getting this wrong in the other direction is the failure mode worth naming:
// a fixed allowlist here would mean every new tenant site needs an Argentum
// deploy, and the pressure that creates is exactly how a `*` ends up in a CORS
// header on a surface that mints sessions.
func EmbedCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		if origin := c.GetHeader("Origin"); origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		// The preflight cache. Ten minutes, so a chatty widget is not
		// re-preflighting every message, and short enough that an admin who has
		// just fixed an allowlist does not wait out a browser cache to see it.
		c.Header("Access-Control-Max-Age", "600")

		if c.Request.Method == http.MethodOptions {
			// Answered for any origin. A preflight carries no body, so there is
			// no key to look up and nothing to check it against; the POST that
			// follows is where the decision is made.
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
