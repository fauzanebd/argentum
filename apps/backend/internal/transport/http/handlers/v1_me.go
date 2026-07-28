package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// V1MeHandler answers `GET /v1/me`: who this key is, what it can do, what it
// is allowed to spend, and which version of the contract it is talking to.
//
// It is the one call an integrator makes before writing any real code, and
// the one paste that makes a support question answerable — which is why it
// carries the rate limit and the credit position rather than making someone
// discover both by being refused. T-13 shipped the identity half early
// because a credential with nothing to authenticate against is untested;
// T-A1 adds the rest without changing what was there.
type V1MeHandler struct {
	companies  *pgctl.CompanyRepo
	budget     V1BudgetReader
	ratePerMin int
}

// V1BudgetReader is the narrow half of app.UsageService this handler needs.
// Declared at the consumer, like ChatEnqueuer's BudgetChecker, so that
// reporting a balance cannot turn into a second way of deciding one. Exported
// because the wiring in cmd/api has to name it to avoid handing this handler
// a nil pointer inside a non-nil interface.
type V1BudgetReader interface {
	CheckBudget(ctx context.Context, companyID string) (app.BudgetState, error)
}

// NewV1MeHandler constructs the handler. companies and budget may be nil; the
// affected block is then omitted rather than the call failing, because the
// key's own identity is what this route is fundamentally for.
func NewV1MeHandler(companies *pgctl.CompanyRepo, budget V1BudgetReader, ratePerMin int) *V1MeHandler {
	return &V1MeHandler{companies: companies, budget: budget, ratePerMin: ratePerMin}
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

	body := gin.H{
		"api_version": apiv1.Version,
		"company":     gin.H{"id": company, "name": name},
		"key": gin.H{
			"id":     c.GetString(middleware.CtxAPIKeyID),
			"name":   c.GetString(middleware.CtxAPIKeyName),
			"scopes": scopes,
		},
		"rate_limit": gin.H{"requests_per_minute": h.ratePerMin},
	}
	if credits := h.credits(c); credits != nil {
		body["credits"] = credits
	}
	c.JSON(http.StatusOK, body)
}

// credits reports the spend position, or nil when it cannot be read.
//
// Dollars rather than the micro-USD the rest of the system counts in: this is
// the one place the number is read by a person rather than summed by a
// machine, and an integrator seeing `24999132` has to be told what it means.
// A lookup failure omits the block instead of failing the call — `/v1/me` is
// what someone hits when things are already going wrong, and it should
// still answer.
func (h *V1MeHandler) credits(c *gin.Context) gin.H {
	if h.budget == nil {
		return nil
	}
	st, err := h.budget.CheckBudget(c.Request.Context(), companyID(c))
	if err != nil {
		return nil
	}
	if !st.Enforced {
		// Neither a balance nor a warning would mean anything here: nothing
		// was consulted. Reporting a $0.00 balance would read as "you are out
		// of credit", which is the opposite of what an unenforced deployment
		// is telling the caller.
		return gin.H{"enforced": false}
	}
	if st.BYOLLM {
		return gin.H{"enforced": true, "byo_llm": true}
	}
	return gin.H{
		"enforced":      true,
		"byo_llm":       false,
		"status":        st.Verdict,
		"balance_usd":   float64(st.BalanceMicroUSD) / 1_000_000,
		"grant_usd":     float64(st.GrantMicroUSD) / 1_000_000,
		"remaining_pct": st.RemainingPct,
	}
}
