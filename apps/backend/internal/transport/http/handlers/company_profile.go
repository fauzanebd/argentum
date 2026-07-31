package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// CompanyProfileHandler is the business profile under Settings (T-B1): what
// this workspace does, read by every agent on every turn.
//
// Read is member, write is admin — the same line T-S1 drew for the roster, and
// for a stronger reason here: this text reaches the system prompt of every
// agent on every channel, so a member who could edit it could rewrite what all
// four agents believe about the company.
type CompanyProfileHandler struct{ svc *app.CompanyProfileService }

// NewCompanyProfileHandler constructs the handler. svc may be nil in
// stripped-down wirings; the routes then answer 503 rather than panicking.
func NewCompanyProfileHandler(svc *app.CompanyProfileService) *CompanyProfileHandler {
	return &CompanyProfileHandler{svc: svc}
}

// Register installs the routes on an authenticated group.
func (h *CompanyProfileHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/company/profile", h.get)
	rg.PUT("/company/profile", h.put)
}

func (h *CompanyProfileHandler) unavailable(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "the business profile is not configured"})
	return true
}

func (h *CompanyProfileHandler) get(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	p, err := h.svc.Get(c.Request.Context(), companyID(c))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		// 200 with an empty form, not 404. A company that has never described
		// itself has not asked for something that is missing — it is in the
		// state every company starts in, and the dashboard renders the same
		// form either way.
		c.JSON(http.StatusOK, profileResponse(emptyProfile(companyID(c)), false))
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, profileResponse(p, true))
	}
}

func (h *CompanyProfileHandler) put(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var in app.ProfileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.svc.Upsert(c.Request.Context(), companyID(c), userID(c), in)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, profileResponse(p, true))
}

// emptyProfile is what a company with no row looks like on the wire: the
// defaults the migration would have written, so the form has values to bind to.
func emptyProfile(companyID string) *domain.CompanyProfile {
	return &domain.CompanyProfile{
		CompanyID:            companyID,
		FiscalYearStartMonth: 1,
		Source:               domain.ProfileSourceHuman,
	}
}

// profileResponse renders the block beside the profile, from the same code the
// turn uses. Both routes answer with it, so a save shows the tenant what their
// edit did to the agent's view without a second request.
func profileResponse(p *domain.CompanyProfile, exists bool) CompanyProfileResponse {
	block, truncated := p.ContextBlock()
	return CompanyProfileResponse{
		Profile:         p,
		Exists:          exists,
		RenderedBlock:   block,
		Truncated:       truncated,
		BlockTokenLimit: domain.CompanyContextMaxTokens,
	}
}
