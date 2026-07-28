package handlers

import (
	"context"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
	"github.com/fauzanebd/argentum/internal/webhookout"
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
	secrets    V1WebhookSecretReader
	ratePerMin int
}

// V1WebhookSecretReader hands back the tenant's callback signing secret,
// minting one on first read (T-A2).
type V1WebhookSecretReader interface {
	EnsureWebhookSecret(ctx context.Context, companyID string) (string, error)
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

// WithWebhookSecrets lets `/v1/me` report the callback signing secret (T-A2).
//
// It is here rather than on a route of its own because this is already the
// call an integrator makes before writing any code, and a secret they need in
// order to verify a callback is exactly the kind of thing they should not have
// to hunt for. Additive to T-A1's contract, which is the only kind of change
// this package permits.
func (h *V1MeHandler) WithWebhookSecrets(r V1WebhookSecretReader) *V1MeHandler {
	h.secrets = r
	return h
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
	if hooks := h.webhooks(c, scopes); hooks != nil {
		body["webhooks"] = hooks
	}
	c.JSON(http.StatusOK, body)
}

// webhooks reports the signing secret, and only to a key that could cause a
// callback in the first place.
//
// Gated on `write:reports` rather than returned to everyone: the secret
// verifies a body we send *because* a report was requested, so a read-only key
// has nothing to verify and no reason to be holding it. A key without the
// scope gets the block omitted rather than an error — nothing about this route
// should fail because of what the caller cannot do.
//
// Minting is lazy and happens here, on the first read by a key that could use
// it, rather than at signup for every company that will never receive one.
func (h *V1MeHandler) webhooks(c *gin.Context, scopes []domain.Scope) gin.H {
	if h.secrets == nil {
		return nil
	}
	if !slices.Contains(scopes, domain.ScopeWriteReports) {
		return nil
	}
	secret, err := h.secrets.EnsureWebhookSecret(c.Request.Context(), companyID(c))
	if err != nil || secret == "" {
		return nil
	}
	return gin.H{
		"signing_secret":   secret,
		"signature_header": webhookout.SignatureHeader,
		"how":              "HMAC-SHA256 over `<t>.<raw body>` where t is the unix timestamp in the header. Compare in constant time and reject a timestamp more than five minutes old.",
	}
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
