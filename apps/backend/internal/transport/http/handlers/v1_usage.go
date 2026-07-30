package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// V1UsageHandler answers `GET /v1/usage`: what this tenant has spent over a
// window, and what they have left (T-A5).
//
// `GET /v1/me` already reports the credit position, and this is not a
// duplicate of it. `/v1/me` answers "can I make a call at all" in one paste;
// this answers "what has my integration cost, over the period I bill my own
// users for" — a window the caller chooses, broken down by model. An
// application reselling Argentum's answers needs the second, and polling
// `/v1/me` would give it one number with no period attached to it.
type V1UsageHandler struct {
	usage  V1UsageReader
	budget V1BudgetReader
}

// V1UsageReader is the narrow half of the usage repository this handler needs.
// Declared at the consumer, like V1BudgetReader beside it, and deliberately
// *not* app.UsageService: that service's summary is hardcoded to the current
// calendar month, which is the dashboard's question rather than an
// integrator's.
type V1UsageReader interface {
	SummaryByCompany(ctx context.Context, companyID string, from, to time.Time) (*domain.UsageSummary, error)
}

// NewV1UsageHandler constructs the handler. Either dependency may be nil: a
// deployment with no usage repository answers a typed 503, and one with no
// credit enforcement omits the credits block rather than failing the call.
func NewV1UsageHandler(usage V1UsageReader, budget V1BudgetReader) *V1UsageHandler {
	return &V1UsageHandler{usage: usage, budget: budget}
}

// Register installs the route. Call on a group already carrying APIKeyAuth.
func (h *V1UsageHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/usage", middleware.RequireScope(domain.ScopeReadUsage), h.usageReport)
}

// maxUsageWindowDays bounds the window. A year is longer than any billing
// period this is for, and the cap exists because the query is a full aggregate
// over an append-only table: an unbounded `from` is a table scan a caller can
// ask for in a loop.
const maxUsageWindowDays = 366

// usageReportResponse is the body of `GET /v1/usage`.
type usageReportResponse struct {
	Period usagePeriodBody `json:"period"`
	Spend  usageSpendBody  `json:"spend"`
	// Credits is omitted when there is nothing to report — a deployment with no
	// credit reader at all. "Enforcement is off" is a value, not an absence,
	// and it is reported as `enforced: false`.
	Credits *usageCreditsBody `json:"credits,omitempty"`
}

// usagePeriodBody is the window the numbers cover. Always echoed, never
// implied: a spend figure with no period attached is a number a caller cannot
// reconcile against anything.
type usagePeriodBody struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type usageSpendBody struct {
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
	// ByModel is absent rather than empty when nothing was spent. It is keyed by
	// the provider's own model id, which is what a caller comparing our bill to
	// their own provider invoice needs.
	ByModel map[string]usageModelSpendBody `json:"by_model,omitempty"`
}

type usageModelSpendBody struct {
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

// usageCreditsBody mirrors `GET /v1/me`'s credits block field for field. Two
// shapes for one concept across one API is a thing integrators trip over, and
// the cost of keeping them identical is this comment plus three pointers.
//
// The numbers are pointers because a zero balance is the single most important
// value this block can carry, and `omitempty` on a float64 would delete it. A
// present `"balance_usd": 0` says "you are out of credit"; an absent one says
// "nobody looked".
type usageCreditsBody struct {
	Enforced   bool     `json:"enforced"`
	BYOLLM     bool     `json:"byo_llm,omitempty"`
	Status     string   `json:"status,omitempty"`
	BalanceUSD *float64 `json:"balance_usd,omitempty"`
	GrantUSD   *float64 `json:"grant_usd,omitempty"`
	// RemainingPct is an integer 0–100, as app.BudgetState reports it and as
	// `GET /v1/me` returns it.
	RemainingPct *int `json:"remaining_pct,omitempty"`
}

func (h *V1UsageHandler) usageReport(c *gin.Context) {
	if h.usage == nil {
		apierr.Abort(c, apierr.TypeServer, "usage_unavailable",
			"Usage reporting is not configured on this deployment.")
		return
	}
	from, to, ok := usageWindow(c)
	if !ok {
		return
	}

	summary, err := h.usage.SummaryByCompany(c.Request.Context(), companyID(c), from, to)
	if err != nil {
		apierr.Abort(c, apierr.TypeServer, "usage_read_failed",
			"Could not read usage for that period. Try again.")
		return
	}

	body := usageReportResponse{
		Period: usagePeriodBody{From: from, To: to},
		Spend: usageSpendBody{
			TokensIn:  summary.TotalTokensIn,
			TokensOut: summary.TotalTokensOut,
			CostUSD:   summary.TotalCostUSD,
			ByModel:   modelSpend(summary),
		},
		Credits: h.creditsBody(c),
	}
	c.JSON(http.StatusOK, body)
}

// usageWindow resolves `from` and `to`, defaulting to the current calendar
// month in UTC — the period a monthly grant is measured against, and the one
// `GET /v1/me` implies.
//
// Both are RFC 3339 and both are refused with `param` naming the one at fault,
// because a caller who sent `2026-07-01` rather than `2026-07-01T00:00:00Z`
// needs to be told which field to fix, not handed a default month's numbers
// that look plausible and answer a different question.
func usageWindow(c *gin.Context) (from, to time.Time, ok bool) {
	now := time.Now().UTC()
	from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to = from.AddDate(0, 1, 0)

	if raw := c.Query("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_timestamp",
				"`from` must be an RFC 3339 timestamp, e.g. 2026-07-01T00:00:00Z.", "from")
			return time.Time{}, time.Time{}, false
		}
		from = t.UTC()
	}
	if raw := c.Query("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_timestamp",
				"`to` must be an RFC 3339 timestamp, e.g. 2026-08-01T00:00:00Z.", "to")
			return time.Time{}, time.Time{}, false
		}
		to = t.UTC()
	}

	if !to.After(from) {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_window",
			"`to` must be after `from`.", "to")
		return time.Time{}, time.Time{}, false
	}
	if to.Sub(from) > maxUsageWindowDays*24*time.Hour {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "window_too_large",
			"The window cannot exceed 366 days. Ask for one period at a time.", "from")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// modelSpend joins the summary's three per-model maps into one object per
// model. Three parallel maps are the shape a SQL aggregate falls out in; one
// object per model is the shape a caller iterates.
func modelSpend(s *domain.UsageSummary) map[string]usageModelSpendBody {
	if s == nil || (len(s.CostByModel) == 0 && len(s.TokensInByModel) == 0) {
		return nil
	}
	out := map[string]usageModelSpendBody{}
	for model := range s.CostByModel {
		out[model] = usageModelSpendBody{
			TokensIn:  s.TokensInByModel[model],
			TokensOut: s.TokensOutByModel[model],
			CostUSD:   s.CostByModel[model],
		}
	}
	// A model can appear in the token maps and not in the cost map — a provider
	// we have no price for records tokens and zero cost. Dropping it would make
	// the per-model tokens disagree with the total.
	for model, tokens := range s.TokensInByModel {
		if _, seen := out[model]; !seen {
			out[model] = usageModelSpendBody{TokensIn: tokens, TokensOut: s.TokensOutByModel[model]}
		}
	}
	return out
}

// creditsBody reports the credit position, or nil when there is no reader.
// Same shape and same failure behaviour as `GET /v1/me`: a lookup that fails
// omits the block rather than failing the call, because the spend numbers above
// it are still true and still what the caller asked for.
func (h *V1UsageHandler) creditsBody(c *gin.Context) *usageCreditsBody {
	if h.budget == nil {
		return nil
	}
	st, err := h.budget.CheckBudget(c.Request.Context(), companyID(c))
	if err != nil {
		return nil
	}
	if !st.Enforced {
		return &usageCreditsBody{Enforced: false}
	}
	if st.BYOLLM {
		return &usageCreditsBody{Enforced: true, BYOLLM: true}
	}
	balance := float64(st.BalanceMicroUSD) / 1_000_000
	grant := float64(st.GrantMicroUSD) / 1_000_000
	remaining := st.RemainingPct
	return &usageCreditsBody{
		Enforced:     true,
		Status:       string(st.Verdict),
		BalanceUSD:   &balance,
		GrantUSD:     &grant,
		RemainingPct: &remaining,
	}
}
