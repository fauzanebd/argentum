package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// V1MeHandler answers `GET /v1/me`: who this key is, and what it can do.
//
// **Why `/v1` has a route at all in T-13.** T-A1 owns the `/v1` contract and
// this endpoint's full shape — it adds the rate limit, the credit balance and
// the API version. What it cannot do is prove T-13, whose gate is a transcript
// of a working call and a revoked one; a credential with nothing to
// authenticate against is untested. So this ships as the smallest possible
// surface, and it is deliberately the one route T-A1 cannot change the meaning
// of: an integrator's first call, answering "is my key live and what does it
// carry?". T-A1 extends the body; it does not replace it.
type V1MeHandler struct{ companies *pgctl.CompanyRepo }

// NewV1MeHandler constructs the handler. companies may be nil; the company
// name is then omitted rather than the call failing, because the key's own
// identity is what this route is for.
func NewV1MeHandler(companies *pgctl.CompanyRepo) *V1MeHandler {
	return &V1MeHandler{companies: companies}
}

// Register installs the route. Call on a group already carrying APIKeyAuth.
func (h *V1MeHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/me", h.me)
}

func (h *V1MeHandler) me(c *gin.Context) {
	company := companyID(c)
	if company == "" {
		apierr.Abort(c, apierr.TypeAuthentication, "missing_api_key",
			"Send your key as `Authorization: Bearer arg_…`.")
		return
	}

	name := ""
	if h.companies != nil {
		if co, err := h.companies.GetByID(c.Request.Context(), company); err == nil {
			name = co.Name
		}
	}

	scopes := middleware.APIKeyScopes(c)
	if scopes == nil {
		scopes = []domain.Scope{}
	}
	c.JSON(http.StatusOK, gin.H{
		"company": gin.H{"id": company, "name": name},
		"key": gin.H{
			"id":     c.GetString(middleware.CtxAPIKeyID),
			"name":   c.GetString(middleware.CtxAPIKeyName),
			"scopes": scopes,
		},
	})
}
