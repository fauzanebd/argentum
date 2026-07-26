package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// LarkHandler exposes per-tenant Lark app configuration and user allowlist
// endpoints. All routes require the auth middleware; the caller is expected
// to wrap the group accordingly.
type LarkHandler struct {
	svc *app.LarkService
}

func NewLarkHandler(svc *app.LarkService) *LarkHandler {
	return &LarkHandler{svc: svc}
}

func (h *LarkHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/lark", h.getCredentials)
	rg.PUT("/lark", h.saveCredentials)
	rg.DELETE("/lark", h.deleteCredentials)
	rg.GET("/lark/users", h.listUsers)
	rg.POST("/lark/users", h.addUser)
	rg.DELETE("/lark/users/:id", h.removeUser)
}

type saveLarkReq struct {
	AppID             string `json:"app_id" binding:"required"`
	AppSecret         string `json:"app_secret"` // empty on rotation keeps existing
	VerificationToken string `json:"verification_token" binding:"required"`
	EncryptKey        string `json:"encrypt_key"`
	BotOpenID         string `json:"bot_open_id"`
	Enabled           *bool  `json:"enabled"`
}

func (h *LarkHandler) saveCredentials(c *gin.Context) {
	var req saveLarkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.svc.SaveCredentials(c.Request.Context(), companyID(c), app.SaveLarkCredentialsInput{
		AppID:             req.AppID,
		AppSecret:         req.AppSecret,
		VerificationToken: req.VerificationToken,
		EncryptKey:        req.EncryptKey,
		BotOpenID:         req.BotOpenID,
		Enabled:           enabled,
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
		"company_id":         row.CompanyID,
		"app_id":             row.AppID,
		"verification_token": row.VerificationToken,
		"encrypt_key":        row.EncryptKey,
		"bot_open_id":        row.BotOpenID,
		"enabled":            row.Enabled,
		"updated_at":         row.UpdatedAt,
	})
}

func (h *LarkHandler) getCredentials(c *gin.Context) {
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
		"configured":         true,
		"company_id":         row.CompanyID,
		"app_id":             row.AppID,
		"verification_token": row.VerificationToken,
		"encrypt_key":        row.EncryptKey,
		"bot_open_id":        row.BotOpenID,
		"enabled":            row.Enabled,
		"updated_at":         row.UpdatedAt,
	})
}

func (h *LarkHandler) deleteCredentials(c *gin.Context) {
	if err := h.svc.DeleteCredentials(c.Request.Context(), companyID(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *LarkHandler) listUsers(c *gin.Context) {
	out, err := h.svc.ListUsers(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

type addLarkUserReq struct {
	LarkOpenID string `json:"lark_open_id" binding:"required"`
	Label      string `json:"label"`
}

func (h *LarkHandler) addUser(c *gin.Context) {
	var req addLarkUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.AddUser(c.Request.Context(), companyID(c), req.LarkOpenID, req.Label); err != nil {
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

func (h *LarkHandler) removeUser(c *gin.Context) {
	if err := h.svc.RemoveUser(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
