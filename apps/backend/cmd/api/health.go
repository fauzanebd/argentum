package main

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

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
	// `/metrics` answers to a credential or to nobody (T-17's first bullet, the
	// half §8b keeps even when the tracing is cut).
	//
	// It used to be served to anyone who asked, minus the per-key labels T-A5
	// added. That was never only route counts: the snapshot carries
	// `llm.cost_total_usd`, token totals and query volumes — this deployment's
	// spend, readable by anyone who can reach the pod. T-A5 narrowed the labels
	// it was adding rather than the exposure it was adding them to, and said so.
	//
	// The rule now:
	//
	//	METRICS_TOKEN set    — the token, or 401. An authorized scrape gets the
	//	                       per-key labels; there is no other way in.
	//	METRICS_TOKEN unset  — loopback only, and without the key labels. 404 to
	//	                       everyone else, because an endpoint that cannot
	//	                       authenticate anybody should not advertise itself.
	//
	// Loopback is decided from the socket's peer address, never from
	// `c.ClientIP()`: gin resolves that through `X-Forwarded-For` by default, so
	// a remote caller would name themselves 127.0.0.1 and be believed. Behind
	// the chart's Traefik the peer is the proxy's pod IP, which is not loopback
	// — a deployment that wants scrapes sets the token, which is the point.
	r.GET("/metrics", func(c *gin.Context) {
		authorized := metricsAuthorized(c, metricsToken)
		if metricsToken != "" && !authorized {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "metrics token required"})
			return
		}
		if metricsToken == "" && !fromLoopback(c.Request.RemoteAddr) {
			c.Status(http.StatusNotFound)
			return
		}
		snapshot := m.GetSnapshot()
		if !authorized {
			snapshot = snapshot.WithoutKeyLabels()
		}
		// Prometheus exposition is the default (T-17). The JSON snapshot is
		// still served, on `?format=json` or an explicit `Accept:
		// application/json`, because it is what a human reads when debugging
		// and what this endpoint has answered since it existed — a scraper
		// asks for the default, and nobody's curl breaks.
		if wantsJSONMetrics(c) {
			c.JSON(http.StatusOK, snapshot)
			return
		}
		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.Status(http.StatusOK)
		if err := snapshot.WriteProm(c.Writer); err != nil {
			logrus.WithError(err).Warn("metrics: exposition write failed mid-scrape")
		}
	})
	if metricsToken == "" {
		logrus.Warn("METRICS_TOKEN is unset: /metrics answers on loopback only and serves no per-key labels")
	}
}

// wantsJSONMetrics reports whether this caller asked for the JSON snapshot
// rather than the exposition format.
func wantsJSONMetrics(c *gin.Context) bool {
	if c.Query("format") == "json" {
		return true
	}
	// Only an explicit application/json counts. A browser sends
	// `Accept: text/html,…,*/*`, and a wildcard is not a request for JSON — it
	// is a request for whatever the endpoint's default is, which is the
	// exposition.
	return strings.Contains(c.GetHeader("Accept"), "application/json")
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

// fromLoopback reports whether a request's peer address is the local machine.
//
// The argument is `http.Request.RemoteAddr` — the TCP peer, which the caller
// cannot choose — and never a header. An address that will not parse is not
// loopback: this decides whether to serve cost data, so the unparseable case
// closes rather than opens.
func fromLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
