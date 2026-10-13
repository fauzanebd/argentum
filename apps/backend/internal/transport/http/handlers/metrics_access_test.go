package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-27: a metric is its source's data in another shape, so a member not
// granted a restricted source is not shown the metrics on it — not in the list,
// and not by id. An admin's reads stay whole, as the source list's do. The
// writes are the role table's and are not touched; the agent half is pinned in
// internal/tools (TestAMetricOnARestrictedSourceStillAnswersAnAgent).

// payrollSQL is the restricted metric's template. The leak the ticket closes is
// a member reading it, so the tests look for any fragment of it in a body.
const payrollSQL = "SELECT SUM(gross_pay) AS v FROM hr.payroll WHERE paid_on >= {{from}} AND paid_on < {{to}}"

// registeredMetrics is a company's metric registry. It embeds the interface, so
// a read route reaching anything but the two reads panics.
type registeredMetrics struct {
	domain.MetricRepository
	rows []*domain.MetricDefinition
}

func (r registeredMetrics) ListByCompany(context.Context, string) ([]*domain.MetricDefinition, error) {
	return r.rows, nil
}

func (r registeredMetrics) GetByID(_ context.Context, _, id string) (*domain.MetricDefinition, error) {
	for _, m := range r.rows {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, domain.ErrNotFound
}

// metricFixture is the ticket's tenant, plus a second metric on the open source
// and one on a granted source: two metrics share a source, so the one question
// is over distinct ids, and the order to keep has a hole in the middle.
func metricFixture() []*domain.MetricDefinition {
	return []*domain.MetricDefinition{
		{ID: "m-revenue", CompanyID: "co-1", SourceID: "src-sales", Key: "net_revenue", Label: "Net revenue",
			SQLTemplate: "SELECT SUM(total) AS v FROM orders WHERE created_at >= {{from}} AND created_at < {{to}}"},
		{ID: "m-payroll", CompanyID: "co-1", SourceID: "src-hr", Key: "payroll_cost", Label: "Payroll cost",
			SQLTemplate: payrollSQL},
		{ID: "m-headcount", CompanyID: "co-1", SourceID: "src-ops", Key: "headcount", Label: "Headcount",
			SQLTemplate: "SELECT COUNT(*) AS v FROM staff WHERE hired_on < {{to}} AND {{from}} IS NOT NULL"},
		{ID: "m-orders", CompanyID: "co-1", SourceID: "src-sales", Key: "order_count", Label: "Orders",
			SQLTemplate: "SELECT COUNT(*) AS v FROM orders WHERE created_at >= {{from}} AND created_at < {{to}}"},
	}
}

func metricsRouter(rows []*domain.MetricDefinition, access *sourceVisibility, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := app.NewMetricService(registeredMetrics{rows: rows}, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-maya")
		c.Set("role", role)
	})
	h := NewMetricsHandler(svc)
	if access != nil {
		h = h.WithAccess(access)
	}
	h.Register(r.Group("/api"))
	return r
}

func getMetrics(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// listedMetricKeys is GET /api/metrics as keys in order, and the raw body.
func listedMetricKeys(t *testing.T, r *gin.Engine) (int, []string, string) {
	t.Helper()
	w := getMetrics(r, "/api/metrics")
	if w.Code != http.StatusOK {
		return w.Code, nil, w.Body.String()
	}
	var body MetricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	keys := make([]string, 0, len(body.Metrics))
	for _, m := range body.Metrics {
		keys = append(keys, m.Key)
	}
	return w.Code, keys, w.Body.String()
}

// leaksPayroll is whether a body carries any trace of the restricted metric:
// its key, its label, its source's id, or a fragment of its SQL.
func leaksPayroll(body string) bool {
	for _, trace := range []string{"payroll_cost", "Payroll cost", "src-hr", "m-payroll", "hr.payroll", "gross_pay"} {
		if strings.Contains(body, trace) {
			return true
		}
	}
	return false
}

// Q1 / AC1, with AC4's grant: what a member sees is decided by the grant alone.
func TestAMembersMetricListFollowsTheSourceGrant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hidden map[string]bool
		want   []string
	}{
		// HR warehouse restricted and not granted; Sales DB open; the ops source
		// granted, which authz reports exactly as it reports an open one.
		{"refused the HR warehouse", map[string]bool{"src-hr": true}, []string{"net_revenue", "headcount", "order_count"}},
		// AC4: the admin grants it, and the metric is back, in its place.
		{"granted the HR warehouse", nil, []string{"net_revenue", "payroll_cost", "headcount", "order_count"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := &sourceVisibility{hidden: tc.hidden}
			code, keys, body := listedMetricKeys(t, metricsRouter(metricFixture(), access, "member"))
			if code != http.StatusOK || !slices.Equal(keys, tc.want) {
				t.Fatalf("status %d, keys %v; want 200 with %v in the registry's order", code, keys, tc.want)
			}
			if tc.hidden["src-hr"] && leaksPayroll(body) {
				t.Errorf("body names the restricted metric, its source or its SQL: %s", body)
			}
			// One question for the page, about sources, each source once.
			if len(access.asked) != 1 || !slices.Equal(access.asked[0], []string{"src-sales", "src-hr", "src-ops"}) {
				t.Fatalf("asked %v; want one question over the distinct source ids", access.asked)
			}
			if s := access.subjects[0]; s.UserID != "u-maya" || s.CompanyID != "co-1" || s.Role != domain.RoleMember {
				t.Errorf("asked for %+v, want the member making the request", s)
			}
		})
	}
}

// Q2 / AC2: an admin with no grant on the HR warehouse still sees, and can
// still open, every metric — Settings → Metrics is where they are edited — and
// nobody is asked.
func TestAnAdminsMetricReadsAreWholeAndAskNothing(t *testing.T) {
	access := &sourceVisibility{hidden: map[string]bool{"src-hr": true}}
	r := metricsRouter(metricFixture(), access, "admin")

	_, keys, _ := listedMetricKeys(t, r)
	if !slices.Equal(keys, []string{"net_revenue", "payroll_cost", "headcount", "order_count"}) {
		t.Errorf("admin lists %v; want every metric", keys)
	}
	if w := getMetrics(r, "/api/metrics/m-payroll"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), payrollSQL[:20]) {
		t.Errorf("admin get = %d %s; want 200 with the metric", w.Code, w.Body.String())
	}
	if len(access.asked) != 0 {
		t.Errorf("asked %v for an admin; want no access read", access.asked)
	}
}

// Q3 / AC3 and AC4: a hidden metric's id is an unknown id, byte for byte; a
// granted one opens.
func TestAMembersHiddenMetricIsNoSuchMetric(t *testing.T) {
	refused := metricsRouter(metricFixture(), &sourceVisibility{hidden: map[string]bool{"src-hr": true}}, "member")
	hidden := getMetrics(refused, "/api/metrics/m-payroll")
	unknown := getMetrics(refused, "/api/metrics/7d1c2f5e-3a4b-4c8d-9e0f-1a2b3c4d5e6f")
	if hidden.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound {
		t.Fatalf("hidden %d, unknown %d; want both 404", hidden.Code, unknown.Code)
	}
	if hidden.Body.String() != unknown.Body.String() || hidden.Body.String() != `{"error":"no such metric"}` {
		t.Errorf("hidden %q, unknown %q; want the same `no such metric` for both", hidden.Body.String(), unknown.Body.String())
	}

	open := getMetrics(refused, "/api/metrics/m-revenue")
	if open.Code != http.StatusOK || !strings.Contains(open.Body.String(), "net_revenue") {
		t.Errorf("open source's metric = %d %s; want 200", open.Code, open.Body.String())
	}

	granted := getMetrics(metricsRouter(metricFixture(), &sourceVisibility{}, "member"), "/api/metrics/m-payroll")
	if granted.Code != http.StatusOK || !strings.Contains(granted.Body.String(), "payroll_cost") {
		t.Errorf("granted get = %d %s; want 200 with the metric", granted.Code, granted.Body.String())
	}
}

// Q4 / AC5: an access read that fails serves nothing — not the list, not the
// metric, and not the storage error.
func TestAMetricAccessCheckThatFailsServesNothing(t *testing.T) {
	r := metricsRouter(metricFixture(), &sourceVisibility{err: errors.New("control DB down")}, "member")
	for _, path := range []string{"/api/metrics", "/api/metrics/m-payroll", "/api/metrics/m-revenue"} {
		w := getMetrics(r, path)
		body := w.Body.String()
		if w.Code != http.StatusServiceUnavailable || body != `{"error":"could not check access; try again"}` {
			t.Errorf("%s = %d %s; want the typed access-check 503", path, w.Code, body)
		}
		if leaksPayroll(body) || strings.Contains(body, "net_revenue") || strings.Contains(body, "control DB") {
			t.Errorf("%s body carries a metric or the storage error: %s", path, body)
		}
	}
}

// Q5: a stack without authz wired reads every metric, as before RIZ-27.
func TestWithNoAccessWiredEveryMetricIsRead(t *testing.T) {
	r := metricsRouter(metricFixture(), nil, "member")
	_, keys, _ := listedMetricKeys(t, r)
	if !slices.Equal(keys, []string{"net_revenue", "payroll_cost", "headcount", "order_count"}) {
		t.Errorf("metrics = %v, want every one", keys)
	}
	if w := getMetrics(r, "/api/metrics/m-payroll"); w.Code != http.StatusOK {
		t.Errorf("get = %d, want 200", w.Code)
	}
}

// Q6 / AC7: a member every metric is hidden from gets the empty registry the
// tab already draws as "No metrics defined yet." — an empty array, not null,
// and the form's grains and units still there.
func TestAMemberShownNoMetricGetsAnEmptyListNotAnError(t *testing.T) {
	access := &sourceVisibility{hidden: map[string]bool{"src-sales": true, "src-hr": true, "src-ops": true}}
	w := getMetrics(metricsRouter(metricFixture(), access, "member"), "/api/metrics")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s, want 200", w.Code, w.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(body["metrics"]) != "[]" {
		t.Errorf("metrics = %s, want []", body["metrics"])
	}
	if len(body["grains"]) < 3 || len(body["units"]) < 3 {
		t.Errorf("grains %s, units %s; want both present", body["grains"], body["units"])
	}

	// A company with no metric at all asks nothing: there is no source to ask about.
	none := &sourceVisibility{}
	if w := getMetrics(metricsRouter(nil, none, "member"), "/api/metrics"); w.Code != http.StatusOK || len(none.asked) != 0 {
		t.Errorf("empty registry = %d after %d questions; want 200 asking nothing", w.Code, len(none.asked))
	}
}
