package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// SlackHandler exposes per-tenant Slack app configuration and user allowlist
// endpoints. All routes require the auth middleware; the caller is expected
// to wrap the group accordingly.
type SlackHandler struct {
	svc *app.SlackService
}

func NewSlackHandler(svc *app.SlackService) *SlackHandler {
	return &SlackHandler{svc: svc}
}

func (h *SlackHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/slack", h.getCredentials)
	rg.PUT("/slack", h.saveCredentials)
	rg.DELETE("/slack", h.deleteCredentials)
	rg.GET("/slack/users", h.listUsers)
	rg.POST("/slack/users", h.addUser)
	rg.DELETE("/slack/users/:id", h.removeUser)
}

type saveSlackReq struct {
	AppID         string `json:"app_id" binding:"required"`
	TeamID        string `json:"team_id"`
	BotToken      string `json:"bot_token"` // empty on rotation keeps existing
	SigningSecret string `json:"signing_secret" binding:"required"`
	BotUserID     string `json:"bot_user_id"`
	Enabled       *bool  `json:"enabled"`
}

func (h *SlackHandler) saveCredentials(c *gin.Context) {
	var req saveSlackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.svc.SaveCredentials(c.Request.Context(), companyID(c), app.SaveSlackCredentialsInput{
		AppID:         req.AppID,
		TeamID:        req.TeamID,
		BotToken:      req.BotToken,
		SigningSecret: req.SigningSecret,
		BotUserID:     req.BotUserID,
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
	c.JSON(http.StatusOK, slackCredResponse(row, true))
}

func (h *SlackHandler) getCredentials(c *gin.Context) {
	row, err := h.svc.GetCredentials(c.Request.Context(), companyID(c))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"configured": false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, slackCredResponse(row, true))
}

// slackCredResponse renders a credential row for the dashboard. signing_secret
// is echoed back (like Lark's verification_token) so the settings form can be
// re-rendered without re-entry; bot_token never leaves the server.
func slackCredResponse(row *domain.CompanySlackCredential, configured bool) gin.H {
	return gin.H{
		"configured":     configured,
		"company_id":     row.CompanyID,
		"app_id":         row.AppID,
		"team_id":        row.TeamID,
		"signing_secret": row.SigningSecret,
		"bot_user_id":    row.BotUserID,
		"enabled":        row.Enabled,
		"updated_at":     row.UpdatedAt,
	}
}

func (h *SlackHandler) deleteCredentials(c *gin.Context) {
	if err := h.svc.DeleteCredentials(c.Request.Context(), companyID(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *SlackHandler) listUsers(c *gin.Context) {
	out, err := h.svc.ListUsers(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

type addSlackUserReq struct {
	SlackUserID string `json:"slack_user_id" binding:"required"`
	Label       string `json:"label"`
}

func (h *SlackHandler) addUser(c *gin.Context) {
	var req addSlackUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.AddUser(c.Request.Context(), companyID(c), req.SlackUserID, req.Label); err != nil {
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

func (h *SlackHandler) removeUser(c *gin.Context) {
	if err := h.svc.RemoveUser(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
