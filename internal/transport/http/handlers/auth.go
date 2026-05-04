// Package handlers wires HTTP requests to application services. Each handler
// is a thin layer: validate input, call the service, render the response.
package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// AuthHandler exposes /auth/* endpoints.
type AuthHandler struct {
	svc        *app.AuthService
	cookieName string
	secure     bool
	refreshTTL time.Duration
}

// NewAuthHandler constructs the handler. `secure` should be true in
// production so the refresh cookie carries the Secure flag.
func NewAuthHandler(svc *app.AuthService, secure bool, refreshTTL time.Duration) *AuthHandler {
	return &AuthHandler{svc: svc, cookieName: "rt", secure: secure, refreshTTL: refreshTTL}
}

// Register installs auth routes onto the supplied router group.
func (h *AuthHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/signup", h.signup)
	rg.POST("/login", h.login)
	rg.POST("/refresh", h.refresh)
	rg.POST("/logout", h.logout)
}

type signupReq struct {
	CompanyName string `json:"company_name" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required"`
}

func (h *AuthHandler) signup(c *gin.Context) {
	var req signupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.svc.Signup(c.Request.Context(), req.CompanyName, req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": "email already in use"})
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	h.setRefreshCookie(c, res.RefreshToken)
	c.JSON(http.StatusCreated, gin.H{
		"company":      res.Company,
		"user":         res.User,
		"access_token": res.AccessToken,
	})
}

type loginReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, domain.ErrCredentialsBad) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.setRefreshCookie(c, res.RefreshToken)
	c.JSON(http.StatusOK, gin.H{
		"user":         res.User,
		"company_id":   res.CompanyID,
		"access_token": res.AccessToken,
	})
}

func (h *AuthHandler) refresh(c *gin.Context) {
	cookie, err := c.Cookie(h.cookieName)
	if err != nil || cookie == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no refresh token"})
		return
	}
	access, err := h.svc.Refresh(c.Request.Context(), cookie)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"access_token": access})
}

func (h *AuthHandler) logout(c *gin.Context) {
	c.SetCookie(h.cookieName, "", -1, "/", "", h.secure, true)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) setRefreshCookie(c *gin.Context, token string) {
	maxAge := int(h.refreshTTL.Seconds())
	// SameSite=Lax keeps the cookie attached to top-level navigations from
	// the dashboard while preventing cross-origin POSTs.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.cookieName, token, maxAge, "/", "", h.secure, true)
}
