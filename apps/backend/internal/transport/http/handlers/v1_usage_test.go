package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// `GET /v1/usage` (T-A5).
//
// What is worth testing here is the window — it is the whole difference between
// this route and the credits block on `/v1/me`, and a wrong default or a
// silently accepted bad timestamp hands the caller a number that answers a
// different question than the one they asked.

type fakeUsageReader struct {
	from, to time.Time
	summary  *domain.UsageSummary
	err      error
}

func (f *fakeUsageReader) SummaryByCompany(
	_ context.Context, _ string, from, to time.Time,
) (*domain.UsageSummary, error) {
	f.from, f.to = from, to
	return f.summary, f.err
}

type fakeBudget struct {
	state app.BudgetState
	err   error
}

func (f fakeBudget) CheckBudget(context.Context, string) (app.BudgetState, error) {
	return f.state, f.err
}

func usageRouter(usage V1UsageReader, budget V1BudgetReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyScopes, []domain.Scope{domain.ScopeReadUsage})
		c.Next()
	})
	NewV1UsageHandler(usage, budget).Register(g)
	return r
}

func getUsage(t *testing.T, r *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/v1/usage"+query, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeUsage(t *testing.T, w *httptest.ResponseRecorder) usageReportResponse {
	t.Helper()
	var body usageReportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return body
}

// TestUsageDefaultsToTheCalendarMonth: the period a monthly grant is measured
// against, and the one `/v1/me` implies.
func TestUsageDefaultsToTheCalendarMonth(t *testing.T) {
	reader := &fakeUsageReader{summary: &domain.UsageSummary{}}
	w := getUsage(t, usageRouter(reader, nil), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	now := time.Now().UTC()
	wantFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !reader.from.Equal(wantFrom) {
		t.Errorf("queried from %s, want %s", reader.from, wantFrom)
	}
	if want := wantFrom.AddDate(0, 1, 0); !reader.to.Equal(want) {
		t.Errorf("queried to %s, want %s", reader.to, want)
	}
	body := decodeUsage(t, w)
	if !body.Period.From.Equal(wantFrom) {
		t.Errorf("echoed period.from = %s, want %s", body.Period.From, wantFrom)
	}
}

// TestUsageHonoursTheRequestedWindow — an application billing its own users
// asks for its own period, not ours.
func TestUsageHonoursTheRequestedWindow(t *testing.T) {
	reader := &fakeUsageReader{summary: &domain.UsageSummary{}}
	w := getUsage(t, usageRouter(reader, nil),
		"?from=2026-06-01T00:00:00Z&to=2026-06-15T12:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := reader.from.Format(time.RFC3339); got != "2026-06-01T00:00:00Z" {
		t.Errorf("from = %s", got)
	}
	if got := reader.to.Format(time.RFC3339); got != "2026-06-15T12:00:00Z" {
		t.Errorf("to = %s", got)
	}
	if got := decodeUsage(t, w).Period.To.Format(time.RFC3339); got != "2026-06-15T12:00:00Z" {
		t.Errorf("echoed to = %s", got)
	}
}

// TestUsageRefusesABadWindow, and names the field. A caller who sent a date
// without a time must be told which parameter to fix rather than handed the
// default month's numbers.
func TestUsageRefusesABadWindow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		code  string
		param string
	}{
		{"date only", "?from=2026-06-01", "invalid_timestamp", "from"},
		{"garbage to", "?to=soon", "invalid_timestamp", "to"},
		{"reversed", "?from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z", "invalid_window", "to"},
		{"equal bounds", "?from=2026-06-01T00:00:00Z&to=2026-06-01T00:00:00Z", "invalid_window", "to"},
		{"too wide", "?from=2020-01-01T00:00:00Z&to=2026-01-01T00:00:00Z", "window_too_large", "from"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeUsageReader{summary: &domain.UsageSummary{}}
			w := getUsage(t, usageRouter(reader, nil), tc.query)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			var body struct {
				Error struct {
					Code  string `json:"code"`
					Param string `json:"param"`
					Type  string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.code)
			}
			if body.Error.Param != tc.param {
				t.Errorf("param = %q, want %q", body.Error.Param, tc.param)
			}
			if !reader.from.IsZero() {
				t.Error("a refused window still queried the database")
			}
		})
	}
}

// TestUsageJoinsThePerModelMaps covers the shape change: three parallel maps out
// of SQL become one object per model, and a model with tokens but no price must
// not vanish — the per-model figures would then disagree with the total.
func TestUsageJoinsThePerModelMaps(t *testing.T) {
	reader := &fakeUsageReader{summary: &domain.UsageSummary{
		TotalTokensIn:  1500,
		TotalTokensOut: 300,
		TotalCostUSD:   0.42,
		CostByModel:    map[string]float64{"claude-opus-5": 0.42},
		TokensInByModel: map[string]int64{
			"claude-opus-5":     1000,
			"unpriced-model-v1": 500,
		},
		TokensOutByModel: map[string]int64{"claude-opus-5": 300},
	}}
	w := getUsage(t, usageRouter(reader, nil), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeUsage(t, w)
	if body.Spend.TokensIn != 1500 || body.Spend.CostUSD != 0.42 {
		t.Errorf("spend = %+v", body.Spend)
	}
	opus, ok := body.Spend.ByModel["claude-opus-5"]
	if !ok {
		t.Fatalf("by_model = %v", body.Spend.ByModel)
	}
	if opus.TokensIn != 1000 || opus.TokensOut != 300 || opus.CostUSD != 0.42 {
		t.Errorf("priced model = %+v", opus)
	}
	unpriced, ok := body.Spend.ByModel["unpriced-model-v1"]
	if !ok {
		t.Fatal("a model with tokens and no price was dropped; the per-model tokens no longer sum to the total")
	}
	if unpriced.TokensIn != 500 || unpriced.CostUSD != 0 {
		t.Errorf("unpriced model = %+v", unpriced)
	}
}

// TestUsageCreditsMirrorMe pins the three states, and specifically that a zero
// balance is *written* rather than omitted — it is the single most important
// value this block can carry.
func TestUsageCreditsMirrorMe(t *testing.T) {
	t.Run("zero balance is present", func(t *testing.T) {
		budget := fakeBudget{state: app.BudgetState{
			Enforced: true, Verdict: app.BudgetExhausted,
			BalanceMicroUSD: 0, GrantMicroUSD: 25_000_000, RemainingPct: 0,
		}}
		w := getUsage(t, usageRouter(&fakeUsageReader{summary: &domain.UsageSummary{}}, budget), "")
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		credits, ok := raw["credits"].(map[string]any)
		if !ok {
			t.Fatalf("no credits block: %s", w.Body.String())
		}
		if _, present := credits["balance_usd"]; !present {
			t.Error("balance_usd was omitted at zero — an absent balance reads as 'nobody looked'")
		}
		if credits["status"] != string(app.BudgetExhausted) {
			t.Errorf("status = %v", credits["status"])
		}
	})

	t.Run("unenforced says so", func(t *testing.T) {
		w := getUsage(t, usageRouter(&fakeUsageReader{summary: &domain.UsageSummary{}},
			fakeBudget{state: app.BudgetState{Enforced: false}}), "")
		credits := decodeUsage(t, w).Credits
		if credits == nil || credits.Enforced {
			t.Fatalf("credits = %+v, want an explicit enforced:false", credits)
		}
		if credits.BalanceUSD != nil {
			t.Error("an unenforced deployment reported a balance; $0.00 there reads as 'out of credit'")
		}
	})

	t.Run("byo llm carries no balance", func(t *testing.T) {
		w := getUsage(t, usageRouter(&fakeUsageReader{summary: &domain.UsageSummary{}},
			fakeBudget{state: app.BudgetState{Enforced: true, BYOLLM: true}}), "")
		credits := decodeUsage(t, w).Credits
		if credits == nil || !credits.BYOLLM || credits.BalanceUSD != nil {
			t.Errorf("credits = %+v", credits)
		}
	})

	t.Run("a failed lookup omits the block, not the spend", func(t *testing.T) {
		reader := &fakeUsageReader{summary: &domain.UsageSummary{TotalTokensIn: 7}}
		w := getUsage(t, usageRouter(reader, fakeBudget{err: context.DeadlineExceeded}), "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the spend is still true", w.Code)
		}
		body := decodeUsage(t, w)
		if body.Credits != nil {
			t.Errorf("credits = %+v, want omitted", body.Credits)
		}
		if body.Spend.TokensIn != 7 {
			t.Errorf("spend was lost with the credits: %+v", body.Spend)
		}
	})
}

// TestUsageWithoutTheScopeIsRefused. The scope is also asserted against the
// spec by cmd/api's behavioural check; this is the handler's own half.
func TestUsageWithoutTheScopeIsRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyScopes, []domain.Scope{domain.ScopeReadThreads})
		c.Next()
	})
	NewV1UsageHandler(&fakeUsageReader{summary: &domain.UsageSummary{}}, nil).Register(g)

	if w := getUsage(t, r, ""); w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}

// TestUsageWithoutAReaderIsATypedFailure — the 503 an integrator can act on,
// rather than a panic or a zero-filled body that looks like a real answer.
func TestUsageWithoutAReaderIsATypedFailure(t *testing.T) {
	w := getUsage(t, usageRouter(nil, nil), "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "usage_unavailable" || body.Error.Type != "server" {
		t.Errorf("error = %+v", body.Error)
	}
}
