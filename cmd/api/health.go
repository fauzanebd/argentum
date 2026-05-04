package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/metrics"
)

func registerHealthRoutes(r *gin.Engine, m *metrics.Collector, controlDB interface{ PingContext(context.Context) error }) {
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
	r.GET("/metrics", func(c *gin.Context) {
		c.JSON(http.StatusOK, m.GetSnapshot())
	})
}
