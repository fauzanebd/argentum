package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// UsageHandler exposes the usage / credits endpoints to the dashboard.
type UsageHandler struct{ svc *app.UsageService }

func NewUsageHandler(svc *app.UsageService) *UsageHandler { return &UsageHandler{svc: svc} }

// Register installs the routes. Call after Auth middleware.
func (h *UsageHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/usage/summary", h.summary)
	rg.GET("/usage/credits", h.credits)
}

func (h *UsageHandler) summary(c *gin.Context) {
	out, err := h.svc.SummaryForCompany(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *UsageHandler) credits(c *gin.Context) {
	out, err := h.svc.CreditsForCompany(c.Request.Context(), companyID(c))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"company_id": companyID(c), "balance_micro_usd": 0})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}
