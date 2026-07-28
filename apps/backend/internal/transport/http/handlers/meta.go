package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// MetaHandler exposes static metadata used by the dashboard onboarding flow.
type MetaHandler struct{}

func NewMetaHandler() *MetaHandler { return &MetaHandler{} }

// Register installs unauthenticated metadata endpoints.
func (h *MetaHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/supported-databases", h.supportedDBs)
}

func (h *MetaHandler) supportedDBs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"supported":  db.Supported,
		"registered": db.Registered(),
	})
}
