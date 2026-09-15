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
	// caps is capability grants (T-Z1). Optional for team's reason: nil
	// answers 503 rather than panicking.
	caps *app.CapabilityService
	// voice is what the caller's own read says this deployment can do (T-W9).
	// The zero value — nothing — is right for every wiring that did not say.
	voice VoiceAvailability
}

// WithVoice states what `me/capabilities` reports about voice. cmd/api passes
// the same Enabled calls that decide whether each voice route is registered, so
// the screen and the router cannot disagree.
func (h *UserHandler) WithVoice(v VoiceAvailability) *UserHandler {
	h.voice = v
	return h
}

// NewUserHandler constructs the handler. team may be nil in stripped-down
// wirings; the team routes then answer 503 rather than panicking.
func NewUserHandler(userRepo *pgctl.UserRepo, companyRepo *pgctl.CompanyRepo, team *app.TeamService) *UserHandler {
	return &UserHandler{userRepo: userRepo, companyRepo: companyRepo, team: team}
}

// WithCapabilities serves the grant routes (T-Z1). They are registered either
// way; without this they answer 503, which says why, where an absent route
// would read as a wrong path.
func (h *UserHandler) WithCapabilities(caps *app.CapabilityService) *UserHandler {
	h.caps = caps
	return h
}

// Register installs user routes onto the supplied router group.
func (h *UserHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/me", h.me)
	rg.GET("", h.list)
	rg.POST("/invite", h.invite)
	rg.PATCH("/:id", h.updateRole)
	rg.DELETE("/:id", h.remove)

	// Capabilities (T-Z1) hang off the user they belong to, beside the role
	// change: a grant is a fact about a person, and the settings page that
	// shows one shows the other.
	rg.GET("/me/capabilities", h.myCapabilities)
	rg.GET("/:id/capabilities", h.userCapabilities)
	rg.PUT("/:id/capabilities/:capability", h.grantCapability)
	rg.DELETE("/:id/capabilities/:capability", h.revokeCapability)
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
	// The plaintext token goes back to the admin who created it **whether or
	// not it was also emailed** (T-F6), and it is returned exactly once —
	// nothing can read it back.
	//
	// Both, rather than one or the other, because `emailed: false` covers three
	// situations an admin cannot tell apart and does not need to: no relay
	// configured, no APP_BASE_URL to build a link with, or a send that failed.
	// In all three the answer is the same as it was before this product could
	// send anything, which is to copy the link.
	c.JSON(http.StatusCreated, gin.H{
		"user":       res.Member,
		"token":      res.Token,
		"emailed":    res.Emailed,
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

// myCapabilities is the one capability read a member may make, and it can only
// name what the caller holds: the user comes from the session, never from the
// path. The dashboard needs it to decide whether a control renders enabled.
func (h *UserHandler) myCapabilities(c *gin.Context) {
	uid := userID(c)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	grants, ok := h.grantsFor(c, uid)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, MyCapabilitiesResponse{Capabilities: grants, Voice: h.voice})
}

func (h *UserHandler) userCapabilities(c *gin.Context) {
	grants, ok := h.grantsFor(c, c.Param("id"))
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"capabilities": grants})
}

// grantsFor reads uid's grants, or writes the refusal and reports false.
func (h *UserHandler) grantsFor(c *gin.Context, uid string) ([]domain.CapabilityGrant, bool) {
	if h.caps == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "capabilities are not configured"})
		return nil, false
	}
	grants, err := h.caps.ListForUser(c.Request.Context(), companyID(c), uid)
	if err != nil {
		writeCapabilityError(c, err)
		return nil, false
	}
	if grants == nil {
		grants = []domain.CapabilityGrant{}
	}
	return grants, true
}

// grantCapability is a PUT because granting is idempotent by decision: asking
// again for what is already held is a 204, not a 409. An admin double-clicking a
// toggle has not caused a conflict.
func (h *UserHandler) grantCapability(c *gin.Context) {
	if h.caps == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "capabilities are not configured"})
		return
	}
	err := h.caps.Grant(c.Request.Context(), companyID(c), userID(c), c.Param("id"), c.Param("capability"))
	if err != nil {
		writeCapabilityError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserHandler) revokeCapability(c *gin.Context) {
	if h.caps == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "capabilities are not configured"})
		return
	}
	err := h.caps.Revoke(c.Request.Context(), companyID(c), userID(c), c.Param("id"), c.Param("capability"))
	if err != nil {
		writeCapabilityError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeCapabilityError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		// A user of another company, a malformed id and a user who never
		// existed are one answer: there is nobody by that id here.
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
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
