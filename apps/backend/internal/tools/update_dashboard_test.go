package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// fakeReviser stands in for app.DashboardService. It records the Input it was
// handed, which is the whole of what this tool decides: everything after the
// merge — validation, ownership, the zero-row dry run — belongs to the service
// and is tested there.
type fakeReviser struct {
	stored  []*domain.Dashboard
	got     dashboard.Input
	gotID   string
	updated bool
	err     error
}

func (f *fakeReviser) Get(_ context.Context, _, id string) (*domain.Dashboard, error) {
	for _, d := range f.stored {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeReviser) List(_ context.Context, _ string) ([]*domain.Dashboard, error) {
	return f.stored, nil
}

func (f *fakeReviser) Update(_ context.Context, _, id string, in dashboard.Input) (*dashboard.SaveResult, error) {
	f.got, f.gotID, f.updated = in, id, true
	if f.err != nil {
		return nil, f.err
	}
	return &dashboard.SaveResult{
		Dashboard: &domain.Dashboard{ID: id, Spec: in.Spec},
		RowCount:  7,
	}, nil
}

func threadOf(s string) *string { return &s }

// storedDashboard is the two-panel dashboard from the 2026-08-17 live gate: a
// KPI, a bar chart, and a period filter whose default window matched no rows.
func storedDashboard() *domain.Dashboard {
	return &domain.Dashboard{
		ID:        "dash-1",
		CompanyID: "co-1",
		ThreadID:  threadOf("thread-1"),
		SourceID:  "src-1",
		Title:     "Penjualan",
		CreatedAt: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
		Spec: spec.Dashboard{
			SpecVersion: spec.Version,
			Title:       "Penjualan",
			SourceID:    "src-1",
			Filters: []spec.Filter{
				{Name: "period", Kind: spec.KindDateRange, Default: string(spec.PresetLast30d)},
			},
			Panels: []spec.Panel{
				{ID: "panel-1", Title: "Revenue", Viz: spec.VizKPI, MetricKey: "revenue",
					Layout: spec.Layout{X: 0, Y: 0, W: 3, H: 2}},
				{ID: "panel-2", Title: "By month", Viz: spec.VizBar,
					SQL:    "SELECT month, revenue FROM v WHERE d >= {{period_from}} AND d < {{period_to}} ORDER BY month",
					Map:    spec.Mapping{Label: "month", Series: []string{"revenue"}},
					Layout: spec.Layout{X: 3, Y: 0, W: 6, H: 4}},
			},
		},
	}
}

func runUpdate(t *testing.T, svc *fakeReviser, threadID, args string) map[string]any {
	t.Helper()
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	if threadID != "" {
		ctx = tenantctx.WithThreadID(ctx, threadID)
	}
	out, err := NewUpdateDashboardTool(svc, nil, nil).Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("tool returned invalid JSON: %v", err)
	}
	return payload
}

func updateErr(t *testing.T, svc *fakeReviser, threadID, args string) string {
	t.Helper()
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	if threadID != "" {
		ctx = tenantctx.WithThreadID(ctx, threadID)
	}
	out, err := NewUpdateDashboardTool(svc, nil, nil).Execute(ctx, args)
	if err == nil {
		t.Fatalf("expected an error, got %s", out)
	}
	return err.Error()
}

// The sentence the ticket was written from: "just make it 2024", with no
// dashboard_id, in the thread that built it.
func TestUpdateResolvesTheThreadsOwnDashboard(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	payload := runUpdate(t, svc, "thread-1", `{
	  "filters": [{"op": "replace", "name": "period", "from": "2024-07-01", "to": "2024-12-31"}]
	}`)

	if svc.gotID != "dash-1" {
		t.Fatalf("edited %q, want dash-1", svc.gotID)
	}
	// Same id, same URL. A link already sent keeps working — which is the whole
	// difference between this and a second create_dashboard.
	if payload["dashboard_id"] != "dash-1" || payload["url"] != "/dashboards/dash-1" {
		t.Errorf("payload = %v", payload)
	}
	if payload["row_count"] != float64(7) {
		t.Errorf("row_count = %v; the fabrication guardrail grounds on it", payload["row_count"])
	}
	got := svc.got.Spec.Filters
	if len(got) != 1 {
		t.Fatalf("filters = %+v", got)
	}
	def, ok := got[0].Default.(map[string]any)
	if !ok || def["from"] != "2024-07-01" || def["to"] != "2024-12-31" {
		t.Errorf("default = %#v, want the 2024 window", got[0].Default)
	}
}

// A patch, not a re-submission: everything the edit does not name survives
// byte-for-byte. This is the property that keeps a one-axis change from being a
// chance for working SQL to come back subtly different.
func TestUpdateLeavesUnnamedPanelsAlone(t *testing.T) {
	before := storedDashboard()
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{
	  "panels": [{"op": "replace", "title": "By month", "viz": "line"}]
	}`)

	got := svc.got.Spec.Panels
	if len(got) != 2 {
		t.Fatalf("panels = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0], before.Spec.Panels[0]) {
		t.Errorf("panel 0 changed: %+v", got[0])
	}
	if got[1].Viz != spec.VizLine {
		t.Errorf("viz = %q, want line", got[1].Viz)
	}
	// The fields the edit did not name — and the id, which the grid and the cache
	// key on.
	if got[1].ID != "panel-2" || got[1].SQL != before.Spec.Panels[1].SQL ||
		got[1].Map.Label != "month" || got[1].Layout != before.Spec.Panels[1].Layout {
		t.Errorf("replace re-emitted the panel instead of patching it: %+v", got[1])
	}
	// `title` addressed the panel; it did not rename it.
	if got[1].Title != "By month" {
		t.Errorf("title = %q, want the address to stay the title", got[1].Title)
	}
}

func TestUpdateAddsAPanelBelowTheExistingOnes(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{
	  "panels": [{"op": "add", "title": "By channel", "viz": "pie",
	              "sql": "SELECT channel, revenue FROM v",
	              "map": {"label": "channel", "value": "revenue"}}]
	}`)

	got := svc.got.Spec.Panels
	if len(got) != 3 {
		t.Fatalf("panels = %d, want 3", len(got))
	}
	// Below everything already placed. A new panel that lands on top of an
	// existing one is a dashboard that looks broken for a reason nobody can see
	// in the JSON.
	if got[2].Layout.Y < 4 {
		t.Errorf("new panel at y=%d overlaps the existing rows", got[2].Layout.Y)
	}
	if got[2].Title != "By channel" || got[2].Viz != spec.VizPie {
		t.Errorf("added panel = %+v", got[2])
	}
}

func TestUpdateRemovesByTitleAndKeepsTheRest(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{"panels": [{"op": "remove", "title": "revenue"}]}`)

	got := svc.got.Spec.Panels
	if len(got) != 1 || got[0].ID != "panel-2" {
		t.Fatalf("panels = %+v, want only panel-2", got)
	}
}

// Naming a panel that does not exist answers with the ones that do — the
// repair-instruction shape sql_error_hint.go uses, which is what turns three
// round trips into one.
func TestUpdateNamesThePanelsThatWouldHaveWorked(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	msg := updateErr(t, svc, "thread-1", `{"panels": [{"op": "replace", "title": "Profit", "viz": "line"}]}`)

	for _, want := range []string{"Profit", "Revenue", "By month"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %q", msg, want)
		}
	}
	if svc.updated {
		t.Error("a refused edit still called Update")
	}
}

// Two panels with one title: refuse rather than edit the first. Editing one of
// two identically named panels looks correct in the reply and wrong on the grid.
func TestUpdateRefusesAnAmbiguousTitle(t *testing.T) {
	d := storedDashboard()
	d.Spec.Panels[1].Title = "Revenue"
	svc := &fakeReviser{stored: []*domain.Dashboard{d}}
	msg := updateErr(t, svc, "thread-1", `{"panels": [{"op": "replace", "title": "Revenue", "viz": "line"}]}`)

	if !strings.Contains(msg, "ambiguous") || !strings.Contains(msg, "index") {
		t.Errorf("error %q should say it is ambiguous and offer the index", msg)
	}
}

func TestUpdateAddressesByIndexWhenAPanelHasNoTitle(t *testing.T) {
	d := storedDashboard()
	d.Spec.Panels[1].Title = ""
	svc := &fakeReviser{stored: []*domain.Dashboard{d}}
	runUpdate(t, svc, "thread-1", `{"panels": [{"op": "replace", "index": 1, "viz": "line"}]}`)

	if svc.got.Spec.Panels[1].Viz != spec.VizLine {
		t.Errorf("panels = %+v", svc.got.Spec.Panels)
	}
}

// Re-pointing a stored dashboard at another warehouse changes what a URL already
// sent serves. Refused, and refused by naming the tool that does do it, so the
// model does not retry the same call with the field removed.
func TestUpdateRefusesToChangeTheSource(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	msg := updateErr(t, svc, "thread-1", `{"source_id": "src-2", "title": "Elsewhere"}`)

	if !strings.Contains(msg, "create_dashboard") {
		t.Errorf("error %q should name create_dashboard as the way to do it", msg)
	}
	if svc.updated {
		t.Error("the stored source was written anyway")
	}
}

// No id and no dashboard from this thread: a RESULT listing the recent ones, not
// a Go error. The 2026-08-14 finding is why — an error to a caller mistake made
// the model re-send the identical call seven times until the budget ended the
// turn.
func TestUpdateAsksWhichDashboardInsteadOfErroring(t *testing.T) {
	older := storedDashboard()
	older.ID, older.ThreadID, older.Title = "dash-0", threadOf("thread-9"), "Older"
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard(), older}}

	payload := runUpdate(t, svc, "thread-2", `{"title": "Anything"}`)

	if payload["needs_dashboard_id"] != true {
		t.Fatalf("payload = %v", payload)
	}
	recent, ok := payload["recent_dashboards"].([]any)
	if !ok || len(recent) != 2 {
		t.Fatalf("recent_dashboards = %v", payload["recent_dashboards"])
	}
	// Stated rather than omitted: nothing was read, so a reply quoting a figure on
	// the back of this call is a fabrication and the guardrail should say so.
	if payload["row_count"] != float64(0) {
		t.Errorf("row_count = %v, want 0", payload["row_count"])
	}
	if svc.updated {
		t.Error("it wrote something while asking which dashboard to write to")
	}
}

func TestUpdateUsesAnExplicitIDFromAnotherThread(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-2", `{"dashboard_id": "dash-1", "title": "Penjualan 2024"}`)

	if svc.gotID != "dash-1" || svc.got.Title != "Penjualan 2024" {
		t.Errorf("id=%q title=%q", svc.gotID, svc.got.Title)
	}
}

// A call that names no edit is refused rather than writing an identical row and
// reporting success — the shape that reads to the user as "it says it fixed it
// and nothing moved".
func TestUpdateRefusesAnEmptyEdit(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	msg := updateErr(t, svc, "thread-1", `{"dashboard_id": "dash-1"}`)

	if !strings.Contains(msg, "nothing to change") {
		t.Errorf("error = %q", msg)
	}
	if svc.updated {
		t.Error("an empty edit still wrote a row")
	}
}

// Switching a panel from SQL to a metric clears the other, because spec.Validate
// refuses both — otherwise the model gets an error about a field it never sent.
func TestUpdateSwitchingSourceKindClearsTheOther(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{"panels": [{"op": "replace", "title": "By month", "metric_key": "revenue_monthly"}]}`)

	p := svc.got.Spec.Panels[1]
	if p.MetricKey != "revenue_monthly" || p.SQL != "" {
		t.Errorf("panel = %+v; a panel carries exactly one of metric_key and sql", p)
	}
}

// The default op is replace: a model that names a panel and some fields and no
// operation means "change this".
func TestUpdateDefaultsToReplace(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{"panels": [{"title": "By month", "viz": "line"}]}`)

	if svc.got.Spec.Panels[1].Viz != spec.VizLine {
		t.Errorf("panels = %+v", svc.got.Spec.Panels)
	}
}

// An add for a filter that already exists is what a model sends when it means
// "make the window 2024". Read as the intent rather than refused for a word.
func TestUpdateFilterAddOnAnExistingNameIsAReplace(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{"filters": [{"op": "add", "name": "period", "kind": "date_range", "default": "ytd"}]}`)

	if n := len(svc.got.Spec.Filters); n != 1 {
		t.Fatalf("filters = %d, want 1", n)
	}
	if svc.got.Spec.Filters[0].Default != "ytd" {
		t.Errorf("default = %v", svc.got.Spec.Filters[0].Default)
	}
}

// A preset stays a string. A stored preset is what keeps the dashboard live; two
// timestamps in its place is a snapshot that ages silently.
func TestUpdateKeepsAPresetAsAPresetName(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	runUpdate(t, svc, "thread-1", `{"filters": [{"op": "replace", "name": "period", "default": "last_month"}]}`)

	if got := svc.got.Spec.Filters[0].Default; got != "last_month" {
		t.Errorf("default = %#v, want the preset name unchanged", got)
	}
}

func TestUpdateNamesTheFiltersThatWouldHaveWorked(t *testing.T) {
	svc := &fakeReviser{stored: []*domain.Dashboard{storedDashboard()}}
	msg := updateErr(t, svc, "thread-1", `{"filters": [{"op": "remove", "name": "region"}]}`)

	if !strings.Contains(msg, "region") || !strings.Contains(msg, "period") {
		t.Errorf("error %q should name what was asked for and what exists", msg)
	}
}

// A deployment with no dashboard service registers the tool anyway — so it
// appears in the agent allowlist and the template vocabulary — and says so if
// executed.
func TestUpdateWithoutAServiceSaysSo(t *testing.T) {
	_, err := NewUpdateDashboardTool(nil, nil, nil).Execute(context.Background(), `{"title": "x"}`)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("err = %v", err)
	}
}
