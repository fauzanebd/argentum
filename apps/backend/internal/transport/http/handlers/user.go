package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

// UserHandler exposes user profile endpoints.
type UserHandler struct {
	userRepo    *pgctl.UserRepo
	companyRepo *pgctl.CompanyRepo
}

// NewUserHandler constructs the handler.
func NewUserHandler(userRepo *pgctl.UserRepo, companyRepo *pgctl.CompanyRepo) *UserHandler {
	return &UserHandler{userRepo: userRepo, companyRepo: companyRepo}
}

// Register installs user routes onto the supplied router group.
func (h *UserHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/me", h.me)
}

func (h *UserHandler) me(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), uid)
	if err != nil {
		if err == domain.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	companyName := ""
	if user.CompanyID != "" {
		company, err := h.companyRepo.GetByID(c.Request.Context(), user.CompanyID)
		if err == nil {
			companyName = company.Name
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":           user.ID,
			"email":        user.Email,
			"name":         user.Email,
			"role":         user.Role,
			"company_id":   user.CompanyID,
			"company_name": companyName,
			"avatar":       nil,
		},
	})
}
