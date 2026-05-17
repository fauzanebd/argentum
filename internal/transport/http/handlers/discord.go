package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DiscordHandler exposes per-tenant Discord bot configuration and user
// allowlist endpoints. All routes require the auth middleware; the caller
// is expected to wrap the group accordingly.
type DiscordHandler struct {
	svc *app.DiscordService
}

func NewDiscordHandler(svc *app.DiscordService) *DiscordHandler {
	return &DiscordHandler{svc: svc}
}

func (h *DiscordHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/discord", h.getCredentials)
	rg.PUT("/discord", h.saveCredentials)
	rg.DELETE("/discord", h.deleteCredentials)
	rg.GET("/discord/users", h.listUsers)
	rg.POST("/discord/users", h.addUser)
	rg.DELETE("/discord/users/:id", h.removeUser)
}

type saveDiscordReq struct {
	ApplicationID string `json:"application_id" binding:"required"`
	PublicKey     string `json:"public_key" binding:"required"`
	BotToken      string `json:"bot_token"` // empty on rotation keeps existing token
	GuildID       string `json:"guild_id"`
	Enabled       *bool  `json:"enabled"`
}

func (h *DiscordHandler) saveCredentials(c *gin.Context) {
	var req saveDiscordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.svc.SaveCredentials(c.Request.Context(), companyID(c), app.SaveCredentialsInput{
		ApplicationID: req.ApplicationID,
		PublicKey:     req.PublicKey,
		BotToken:      req.BotToken,
		GuildID:       req.GuildID,
		Enabled:       enabled,
	})
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"company_id":     row.CompanyID,
		"application_id": row.ApplicationID,
		"public_key":     row.PublicKey,
		"guild_id":       row.GuildID,
		"enabled":        row.Enabled,
		"updated_at":     row.UpdatedAt,
	})
}

func (h *DiscordHandler) getCredentials(c *gin.Context) {
	row, err := h.svc.GetCredentials(c.Request.Context(), companyID(c))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"configured": false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"configured":     true,
		"company_id":     row.CompanyID,
		"application_id": row.ApplicationID,
		"public_key":     row.PublicKey,
		"guild_id":       row.GuildID,
		"enabled":        row.Enabled,
		"updated_at":     row.UpdatedAt,
	})
}

func (h *DiscordHandler) deleteCredentials(c *gin.Context) {
	if err := h.svc.DeleteCredentials(c.Request.Context(), companyID(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DiscordHandler) listUsers(c *gin.Context) {
	out, err := h.svc.ListUsers(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

type addDiscordUserReq struct {
	DiscordUserID string `json:"discord_user_id" binding:"required"`
	Label         string `json:"label"`
}

func (h *DiscordHandler) addUser(c *gin.Context) {
	var req addDiscordUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.AddUser(c.Request.Context(), companyID(c), req.DiscordUserID, req.Label); err != nil {
		switch {
		case errors.Is(err, domain.ErrAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": "user already on allowlist"})
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.Status(http.StatusCreated)
}

func (h *DiscordHandler) removeUser(c *gin.Context) {
	if err := h.svc.RemoveUser(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
