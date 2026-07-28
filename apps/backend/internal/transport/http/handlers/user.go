package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// UserHandler exposes the caller's own profile plus company team management.
// The team routes are admin-only; that is enforced by the route policy in
// cmd/api, not here, so every gated route in the product is listed in one
// place that a test can enumerate.
type UserHandler struct {
	userRepo    *pgctl.UserRepo
	companyRepo *pgctl.CompanyRepo
	team        *app.TeamService
}

// NewUserHandler constructs the handler. team may be nil in stripped-down
// wirings; the team routes then answer 503 rather than panicking.
func NewUserHandler(userRepo *pgctl.UserRepo, companyRepo *pgctl.CompanyRepo, team *app.TeamService) *UserHandler {
	return &UserHandler{userRepo: userRepo, companyRepo: companyRepo, team: team}
}

// Register installs user routes onto the supplied router group.
func (h *UserHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/me", h.me)
	rg.GET("", h.list)
	rg.POST("/invite", h.invite)
	rg.PATCH("/:id", h.updateRole)
	rg.DELETE("/:id", h.remove)
}

func (h *UserHandler) me(c *gin.Context) {
	uid := userID(c)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), uid)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
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

func (h *UserHandler) list(c *gin.Context) {
	if h.team == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "team management is not configured"})
		return
	}
	members, err := h.team.List(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": members})
}

type inviteReq struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required"`
}

func (h *UserHandler) invite(c *gin.Context) {
	if h.team == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "team management is not configured"})
		return
	}
	var req inviteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.team.Invite(c.Request.Context(), companyID(c), userID(c), req.Email, domain.Role(req.Role))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": "that email already has an account"})
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	// There is no mail transport in the product yet, so the plaintext token
	// goes back to the admin who created it and they pass on the link
	// themselves. It is returned exactly once: nothing can read it back.
	c.JSON(http.StatusCreated, gin.H{
		"user":       res.Member,
		"token":      res.Token,
		"expires_at": res.ExpiresAt,
	})
}

type updateRoleReq struct {
	Role string `json:"role" binding:"required"`
}

func (h *UserHandler) updateRole(c *gin.Context) {
	if h.team == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "team management is not configured"})
		return
	}
	var req updateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	err := h.team.ChangeRole(c.Request.Context(), companyID(c), c.Param("id"), domain.Role(req.Role))
	if err != nil {
		writeTeamError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserHandler) remove(c *gin.Context) {
	if h.team == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "team management is not configured"})
		return
	}
	if err := h.team.Remove(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		writeTeamError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeTeamError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrLastAdmin):
		// 409, not 403: the caller has the right role, the company is in a
		// state that forbids the transition.
		c.JSON(http.StatusConflict, gin.H{"error": "this is the last admin; promote someone else first"})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
