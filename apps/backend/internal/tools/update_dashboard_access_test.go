package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// T-Z12: update_dashboard asks, for the person on the turn, whether they may open
// a dashboard before it names one or edits one. T-Z5 closed a restricted
// dashboard on every dashboard route and left this tool naming and editing any of
// them for anyone who asked an agent.

// dashboardGrants is authz for dashboards, with a record of what it was asked.
// A hidden id is restricted and not granted; every other id is reachable.
type dashboardGrants struct {
	hidden   map[string]bool
	err      error
	decided  []string
	listed   [][]string
	subjects []authz.Subject
}

func (g *dashboardGrants) Decide(_ context.Context, s authz.Subject, kind domain.ResourceKind, id string) (authz.Decision, error) {
	g.decided = append(g.decided, id)
	g.subjects = append(g.subjects, s)
	if kind != domain.ResourceKindDashboard {
		return authz.Decision{}, fmt.Errorf("asked about %s, not dashboards", kind)
	}
	if g.err != nil {
		return authz.Decision{}, g.err
	}
	if g.hidden[id] {
		return authz.Decision{Reason: authz.ReasonNotGranted}, nil
	}
	return authz.Decision{Allowed: true, Reason: authz.ReasonGranted}, nil
}

func (g *dashboardGrants) Visible(_ context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	g.listed = append(g.listed, slices.Clone(ids))
	g.subjects = append(g.subjects, s)
	if kind != domain.ResourceKindDashboard {
		return nil, fmt.Errorf("asked about %s, not dashboards", kind)
	}
	if g.err != nil {
		return nil, g.err
	}
	out := []string{}
	for _, id := range ids {
		if !g.hidden[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (g *dashboardGrants) asked() int { return len(g.decided) + len(g.listed) }

// runAs is one update_dashboard call on a turn. An empty userID is a turn with no
// person — a channel, a key, the widget, a watcher.
func runAs(grants *dashboardGrants, svc *fakeReviser, userID, threadID, args string) (string, error) {
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	if userID != "" {
		ctx = tenantctx.WithUserID(ctx, userID)
	}
	if threadID != "" {
		ctx = tenantctx.WithThreadID(ctx, threadID)
	}
	return NewUpdateDashboardTool(svc, nil, nil).WithAccess(grants).Execute(ctx, args)
}

func decoded(t *testing.T, out string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("tool returned invalid JSON: %v\n%s", err, out)
	}
	return payload
}

// payroll is a restricted dashboard an HR conversation built. Its title, its
// panel titles and its filter name are what a person refused it must never read
// back out of this tool.
func payroll() *domain.Dashboard {
	d := storedDashboard()
	d.ID, d.Title, d.ThreadID = "dash-payroll", "Payroll by department", threadOf("thread-hr")
	d.Spec.Title = d.Title
	d.Spec.Panels[0].Title = "Salary total"
	d.Spec.Panels[1].Title = "Salary by department"
	d.Spec.Filters[0].Name = "pay_period"
	return d
}

// assertRefused is the whole shape of a refusal: a result the model reads rather
// than a Go error, nothing written, and nothing of the dashboard in it.
func assertRefused(t *testing.T, svc *fakeReviser, out string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("a refusal is a result the model reads, not a Go error: %v", err)
	}
	if svc.updated {
		t.Fatalf("a restricted dashboard was edited: %q", svc.gotID)
	}
	payload := decoded(t, out)
	if payload["restricted"] != true || payload["row_count"] != float64(0) {
		t.Errorf("payload = %v, want restricted and row_count 0", payload)
	}
	if msg, _ := payload["error"].(string); !strings.Contains(msg, "restricted") {
		t.Errorf("error = %q, want it to say the dashboard is restricted", msg)
	}
	for _, secret := range []string{"Payroll", "Salary", "pay_period", "dash-payroll"} {
		if strings.Contains(out, secret) {
			t.Errorf("the refusal carries %q: %s", secret, out)
		}
	}
}

// §14d's sequence, second half: "change Payroll", by id. The edit names a panel
// Payroll does not have, which before this ticket answered with every panel it
// does have — so the check has to come before the edit is read.
func TestAPersonNotGrantedADashboardCannotEditItByID(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard(), payroll()}}
	grants := &dashboardGrants{hidden: map[string]bool{"dash-payroll": true}}

	out, err := runAs(grants, svc, "u-member", "thread-1", `{
	  "dashboard_id": "dash-payroll",
	  "panels": [{"op": "replace", "title": "Headcount", "viz": "bar"}]
	}`)

	assertRefused(t, svc, out, err)
	if !slices.Equal(grants.decided, []string{"dash-payroll"}) || len(grants.listed) != 0 {
		t.Errorf("decided %v and listed %v, want one decision about dash-payroll", grants.decided, grants.listed)
	}
	if s := grants.subjects[0]; s.CompanyID != "co-1" || s.UserID != "u-member" {
		t.Errorf("asked for %+v, want the turn's company and person", s)
	}
}

// The failure the refusal must not become: T-Q13's evidence check counting it as
// an edit, so a reply saying "done" passes as evidenced. agentbudget reads an
// `error` key as a failed call.
func TestARefusalIsAFailedCallNotAnEdit(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{payroll()}}
	refused, err := runAs(&dashboardGrants{hidden: map[string]bool{"dash-payroll": true}}, svc, "u-member", "",
		`{"dashboard_id": "dash-payroll", "title": "Gaji"}`)
	if err != nil {
		t.Fatal(err)
	}
	tracker := agentbudget.New(agentbudget.Budget{}.Normalize())
	tracker.Observe("update_dashboard", refused, nil)
	if got := tracker.Snapshot().Succeeded; len(got) != 0 {
		t.Errorf("a refused edit counted as succeeded: %v", got)
	}

	// The control: the same call by somebody granted it is an edit, and counts.
	edited, err := runAs(&dashboardGrants{}, svc, "u-granted", "", `{"dashboard_id": "dash-payroll", "title": "Gaji"}`)
	if err != nil {
		t.Fatal(err)
	}
	granted := agentbudget.New(agentbudget.Budget{}.Normalize())
	granted.Observe("update_dashboard", edited, nil)
	if got := granted.Snapshot().Succeeded; !slices.Equal(got, []string{"update_dashboard"}) {
		t.Errorf("a granted edit did not count as succeeded: %v", got)
	}
}

// The default is the newest dashboard this conversation built. When that one is
// refused, the tool must not fall through to an older one from the same
// conversation: the model means the one it just built, and an edit that lands on
// a different dashboard looks right in the result and wrong on the grid.
func TestTheConversationsOwnRestrictedDashboardIsRefusedNotSwapped(t *testing.T) {
	older := storedDashboard()
	older.ID, older.ThreadID = "dash-older", threadOf("thread-hr")
	svc := &fakeReviser{stored: []*domain.Dashboard{payroll(), older}} // newest first
	grants := &dashboardGrants{hidden: map[string]bool{"dash-payroll": true}}

	out, err := runAs(grants, svc, "u-member", "thread-hr", `{"title": "Gaji per departemen"}`)

	assertRefused(t, svc, out, err)
	if !slices.Equal(grants.decided, []string{"dash-payroll"}) || len(grants.listed) != 0 {
		t.Errorf("decided %v and listed %v, want one decision about the conversation's newest", grants.decided, grants.listed)
	}
}

func named(id, title string) *domain.Dashboard {
	return &domain.Dashboard{ID: id, CompanyID: "co-1", Title: title,
		CreatedAt: time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)}
}

func recentIDs(t *testing.T, payload map[string]any) []string {
	t.Helper()
	if payload["needs_dashboard_id"] != true {
		t.Fatalf("want an ask, got %v", payload)
	}
	list, _ := payload["recent_dashboards"].([]any)
	ids := []string{}
	for _, entry := range list {
		m, _ := entry.(map[string]any)
		id, _ := m["dashboard_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// §14d's sequence, first half: "which dashboards are there?". The ask is a list,
// so it hides — the dashboards page's rule — and a hidden dashboard does not take
// one of the five places: the person is offered five they can open.
func TestTheAskListOmitsWhatThePersonMayNotOpen(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{
		named("dash-a", "Sales"), named("dash-payroll", "Payroll by department"), named("dash-c", "Stock"),
		named("dash-d", "Returns"), named("dash-e", "Churn"), named("dash-f", "Margin"),
	}}
	grants := &dashboardGrants{hidden: map[string]bool{"dash-payroll": true}}

	out, err := runAs(grants, svc, "u-member", "thread-new", `{"title": "x"}`)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := recentIDs(t, decoded(t, out)), []string{"dash-a", "dash-c", "dash-d", "dash-e", "dash-f"}; !slices.Equal(got, want) {
		t.Errorf("offered %v, want %v", got, want)
	}
	if strings.Contains(out, "Payroll") {
		t.Errorf("the ask names the hidden dashboard: %s", out)
	}
	// One load for the whole list, however long it is — not one per dashboard.
	if len(grants.listed) != 1 || len(grants.listed[0]) != 6 || len(grants.decided) != 0 {
		t.Errorf("listed %v and decided %v, want one Visible over all six", grants.listed, grants.decided)
	}
	if svc.updated {
		t.Error("an ask edited something")
	}
}

// Hidden reads as absent, all the way down: a person who may open none of the
// company's dashboards is answered exactly as a company with none is.
func TestWhenEveryDashboardIsHiddenTheAskIsTheEmptyWorkspaces(t *testing.T) {
	hiddenAll, err := runAs(&dashboardGrants{hidden: map[string]bool{"dash-payroll": true}},
		&fakeReviser{stored: []*domain.Dashboard{named("dash-payroll", "Payroll by department")}},
		"u-member", "", `{"title": "x"}`)
	if err != nil {
		t.Fatal(err)
	}
	none, err := runAs(&dashboardGrants{}, &fakeReviser{}, "u-member", "", `{"title": "x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if hiddenAll != none {
		t.Errorf("hidden and absent answer differently:\n hidden: %s\n   none: %s", hiddenAll, none)
	}
}

// A grant is what opens a restricted dashboard, to this tool as to the routes.
func TestAGrantedPersonEditsARestrictedDashboard(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{payroll()}}
	grants := &dashboardGrants{} // Decide answers granted

	payload := decoded(t, mustRun(t, grants, svc, "u-granted", "thread-hr", `{"title": "Gaji per departemen"}`))

	if !svc.updated || svc.gotID != "dash-payroll" || payload["updated"] != true {
		t.Errorf("updated=%v id=%q payload=%v, want dash-payroll edited", svc.updated, svc.gotID, payload)
	}
	if !slices.Equal(grants.decided, []string{"dash-payroll"}) {
		t.Errorf("decided %v, want dash-payroll", grants.decided)
	}
}

// Decision 7, asserted rather than assumed: a turn with no person — a channel, a
// `/v1` key, the widget, a watcher — asks nothing and edits what it edited before
// roadmap 12. What those doors should do is T-Z8's.
func TestATurnWithNoPersonAsksNothing(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{payroll(), named("dash-a", "Sales")}}
	grants := &dashboardGrants{hidden: map[string]bool{"dash-payroll": true}}

	mustRun(t, grants, svc, "", "", `{"dashboard_id": "dash-payroll", "title": "Gaji"}`)
	if !svc.updated || svc.gotID != "dash-payroll" {
		t.Errorf("updated=%v id=%q, want the edit a channel turn has always made", svc.updated, svc.gotID)
	}
	ask := decoded(t, mustRun(t, grants, svc, "", "thread-new", `{"title": "x"}`))
	if got := recentIDs(t, ask); !slices.Equal(got, []string{"dash-payroll", "dash-a"}) {
		t.Errorf("offered %v, want the unfiltered list", got)
	}
	if grants.asked() != 0 {
		t.Errorf("a turn with no person asked authz %d times", grants.asked())
	}
}

// A check that cannot be made refuses — the edit and the list both — and says to
// try again without handing the model the storage error.
func TestAnAccessCheckThatFailsChangesAndListsNothing(t *testing.T) {
	grants := &dashboardGrants{err: errors.New("pq: connection refused to 10.0.0.7")}
	svc := &fakeReviser{stored: []*domain.Dashboard{payroll()}}

	for name, args := range map[string]string{
		"by id":   `{"dashboard_id": "dash-payroll", "title": "Gaji"}`,
		"the ask": `{"title": "Gaji"}`,
		"default": `{"title": "Gaji"}`,
	} {
		thread := "thread-new"
		if name == "default" {
			thread = "thread-hr"
		}
		out, err := runAs(grants, svc, "u-member", thread, args)
		if err == nil {
			t.Errorf("%s: a failed check answered %s", name, out)
			continue
		}
		if strings.Contains(err.Error(), "10.0.0.7") {
			t.Errorf("%s: the storage error reached the model: %v", name, err)
		}
	}
	if svc.updated {
		t.Error("a failed check edited a dashboard")
	}
}

func mustRun(t *testing.T, grants *dashboardGrants, svc *fakeReviser, userID, threadID, args string) string {
	t.Helper()
	out, err := runAs(grants, svc, userID, threadID, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out
}
