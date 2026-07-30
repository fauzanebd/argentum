package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/metrics"
)

func registerHealthRoutes(
	r *gin.Engine,
	m *metrics.Collector,
	controlDB interface{ PingContext(context.Context) error },
	metricsToken string,
) {
	if m == nil {
		// A wiring that forgot the collector would otherwise 500 on the one
		// endpoint whose job is to work when other things do not.
		m = metrics.Default()
	}
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "timestamp": time.Now().Unix()})
	})
	r.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := controlDB.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ready": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ready": true})
	})
	// `/metrics` is unauthenticated when METRICS_TOKEN is unset, which is how it
	// has always been served and which T-17 is the ticket for fixing properly —
	// off the public router, on an internal listener.
	//
	// What T-A5 must not do in the meantime is *widen* it. Its new per-key block
	// is labelled by API key id — a tenant's own identifier for a credential
	// they hold — so those labels go out only to a caller that presented the
	// token. Everyone else gets the endpoint they already had, plus route-level
	// numbers, which name no tenant.
	//
	// T-A5's own ticket says "`/metrics` is secured by `T-05`; do not add this
	// before that lands". T-05 was the agent audit log and secured nothing here;
	// the sentence was wrong when it was written. This is the smallest thing
	// that makes the instruction true for the data being added.
	r.GET("/metrics", func(c *gin.Context) {
		snapshot := m.GetSnapshot()
		if !metricsAuthorized(c, metricsToken) {
			snapshot = snapshot.WithoutKeyLabels()
		}
		c.JSON(http.StatusOK, snapshot)
	})
}

// metricsAuthorized reports whether the caller presented the metrics token.
//
// An unset token is never a match: it must not be the case that leaving the
// setting empty turns every scrape into an authorized one. The comparison is
// constant-time because this is a shared secret arriving over HTTP.
func metricsAuthorized(c *gin.Context, token string) bool {
	if token == "" {
		return false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}
