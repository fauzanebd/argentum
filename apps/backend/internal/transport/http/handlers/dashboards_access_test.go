package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z5: the dashboard list is narrowed to what the person reading it may open.
// Opening one by id is resourcePolicy's, proven against the real router in
// cmd/api; the list carries no id, so it asks in the handler — once for the
// page, and for an admin exactly as for a member.

// listedDashboards is a company's dashboards, newest first. It embeds the
// interface, so a list route reaching anything but ListByCompany panics.
type listedDashboards struct {
	domain.DashboardRepository
	rows []*domain.Dashboard
}

func (d listedDashboards) ListByCompany(context.Context, string) ([]*domain.Dashboard, error) {
	return d.rows, nil
}

// dashboardVisibility is authz.Visible with a record of what it was asked.
type dashboardVisibility struct {
	hidden   map[string]bool
	err      error
	asked    [][]string
	subjects []authz.Subject
}

func (v *dashboardVisibility) Visible(_ context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	v.asked = append(v.asked, slices.Clone(ids))
	v.subjects = append(v.subjects, s)
	if kind != domain.ResourceKindDashboard {
		return nil, fmt.Errorf("asked about %s, not dashboards", kind)
	}
	if v.err != nil {
		return nil, v.err
	}
	out := []string{}
	for _, id := range ids {
		if !v.hidden[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// threeDashboards has Payroll created by the very person reading the list, so a
// test that hides it proves created_by opens nothing (056:50).
func threeDashboards() []*domain.Dashboard {
	dewi := "u-dewi"
	return []*domain.Dashboard{
		{ID: "payroll", CompanyID: "co-1", Title: "Payroll by department", CreatedBy: &dewi, AccessMode: domain.AccessModeRestricted},
		{ID: "sales", CompanyID: "co-1", Title: "Weekly sales", AccessMode: domain.AccessModeOpen},
		{ID: "targets", CompanyID: "co-1", Title: "Branch targets", AccessMode: domain.AccessModeOpen},
	}
}

func dashboardsRouter(rows []*domain.Dashboard, access DashboardAccess, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-dewi")
		c.Set("role", role)
	})
	h := NewNativeDashboardsHandler(app.NewDashboardService(listedDashboards{rows: rows}, nil, nil))
	if access != nil {
		h = h.WithAccess(access)
	}
	h.Register(r.Group("/api"))
	return r
}

func listedDashboardIDs(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Dashboards []struct {
			ID string `json:"id"`
		} `json:"dashboards"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	ids := make([]string, 0, len(body.Dashboards))
	for _, d := range body.Dashboards {
		ids = append(ids, d.ID)
	}
	return ids
}

func TestTheDashboardListOmitsWhatThePersonMayNotOpen(t *testing.T) {
	for _, role := range []string{"member", "admin"} {
		t.Run(role, func(t *testing.T) {
			access := &dashboardVisibility{hidden: map[string]bool{"payroll": true}}
			w := serve(dashboardsRouter(threeDashboards(), access, role), http.MethodGet, "/api/dashboards")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if got := listedDashboardIDs(t, w); !slices.Equal(got, []string{"sales", "targets"}) {
				t.Errorf("listed %v, want the two open dashboards in order — Payroll's creator reading the list included", got)
			}
			if strings.Contains(w.Body.String(), "Payroll") {
				t.Errorf("a dashboard the person may not open is named in the list: %s", w.Body.String())
			}
			// One question for the page, about every dashboard on it, from the
			// person reading it.
			if len(access.asked) != 1 {
				t.Fatalf("asked %d times (%v), want once for the page", len(access.asked), access.asked)
			}
			if !slices.Equal(access.asked[0], []string{"payroll", "sales", "targets"}) {
				t.Errorf("asked about %v, want the whole page in order", access.asked[0])
			}
			if s := access.subjects[0]; s.UserID != "u-dewi" || s.CompanyID != "co-1" {
				t.Errorf("asked for %+v, want the caller", s)
			}
		})
	}
}

func TestWithNothingRestrictedEveryDashboardIsListed(t *testing.T) {
	all := []string{"payroll", "sales", "targets"}
	for name, access := range map[string]DashboardAccess{
		"not wired":         nil,
		"nothing is hidden": &dashboardVisibility{},
	} {
		t.Run(name, func(t *testing.T) {
			w := serve(dashboardsRouter(threeDashboards(), access, "member"), http.MethodGet, "/api/dashboards")
			if got := listedDashboardIDs(t, w); !slices.Equal(got, all) {
				t.Errorf("listed %v, want every dashboard as before: %v", got, all)
			}
		})
	}

	// A company with no dashboards asks nothing and still answers a list, not null.
	access := &dashboardVisibility{}
	w := serve(dashboardsRouter(nil, access, "member"), http.MethodGet, "/api/dashboards")
	if w.Body.String() != `{"dashboards":[]}` || len(access.asked) != 0 {
		t.Errorf("no dashboards: body %s, asked %v — want an empty list and no question", w.Body.String(), access.asked)
	}
}

func TestADashboardListCheckThatFailsServesNothing(t *testing.T) {
	access := &dashboardVisibility{err: errors.New("control database unreachable")}
	w := serve(dashboardsRouter(threeDashboards(), access, "admin"), http.MethodGet, "/api/dashboards")
	body := w.Body.String()
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(body, "could not check dashboard access") {
		t.Errorf("status %d %s, want the access check's 503", w.Code, body)
	}
	for _, leak := range []string{"Payroll", "Weekly sales", "unreachable"} {
		if strings.Contains(body, leak) {
			t.Errorf("a failed check leaked %q: %s", leak, body)
		}
	}
}
