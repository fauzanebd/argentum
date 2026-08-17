package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
)

type fakeCreator struct {
	got dashboard.Input
	res *dashboard.SaveResult
	err error
}

func (f *fakeCreator) Create(_ context.Context, _, _ string, in dashboard.Input) (*dashboard.SaveResult, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	if f.res != nil {
		return f.res, nil
	}
	return &dashboard.SaveResult{
		Dashboard: &domain.Dashboard{ID: "dash-1", Spec: in.Spec},
		RowCount:  12,
	}, nil
}

func runTool(t *testing.T, creator *fakeCreator, args string) map[string]any {
	t.Helper()
	out, err := NewCreateDashboardTool(creator, nil, nil).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("tool returned invalid JSON: %v", err)
	}
	return payload
}

// The shape the description asks for, end to end: one call, several panels, a
// filter the panels bind.
func TestCreateDashboardBuildsASpecFromOneCall(t *testing.T) {
	creator := &fakeCreator{}
	payload := runTool(t, creator, `{
	  "title": "Penjualan",
	  "source_id": "src-1",
	  "timezone": "Asia/Jakarta",
	  "filters": [{"name": "period", "kind": "date_range", "default": "last_30d"}],
	  "panels": [
	    {"title": "Revenue", "viz": "kpi", "metric_key": "revenue"},
	    {"title": "By month", "viz": "bar",
	     "sql": "SELECT month, revenue FROM v WHERE d >= {{period_from}} AND d < {{period_to}} ORDER BY month",
	     "map": {"label": "month", "series": ["revenue"]}}
	  ]
	}`)

	if payload["dashboard_id"] != "dash-1" {
		t.Errorf("payload = %v", payload)
	}
	if payload["url"] != "/dashboards/dash-1" {
		t.Errorf("url = %v", payload["url"])
	}
	// row_count grounds the reply. Without it the fabrication guardrail has no
	// evidence the turn touched data, and the metric registry's gate recorded
	// what that costs: every answer suppressed.
	if payload["row_count"] != float64(12) {
		t.Errorf("row_count = %v, want the rows the panels returned", payload["row_count"])
	}

	sp := creator.got.Spec
	if sp.SpecVersion != spec.Version || sp.SourceID != "src-1" || sp.TimeZone != "Asia/Jakarta" {
		t.Errorf("spec = %+v", sp)
	}
	if len(sp.Panels) != 2 || sp.Panels[0].Viz != spec.VizKPI || sp.Panels[1].Viz != spec.VizBar {
		t.Fatalf("panels = %+v", sp.Panels)
	}
	if sp.Panels[0].ID == "" || sp.Panels[0].ID == sp.Panels[1].ID {
		t.Error("panels need distinct ids; the cache and the grid key on them")
	}
	if len(sp.Filters) != 1 || sp.Filters[0].Kind != spec.KindDateRange {
		t.Fatalf("filters = %+v", sp.Filters)
	}
}

// Every tolerance here is a shape the pair this replaced was actually sent.
// Rejecting them costs a turn's iteration budget to teach the model something
// the parser can simply accept.
func TestCreateDashboardToleratesWhatModelsActuallySend(t *testing.T) {
	creator := &fakeCreator{}
	runTool(t, creator, `{
	  "name": "Sales",
	  "cards": [
	    {"title": "Trend", "chart_type": "Grouped Bar",
	     "query": "SELECT month, revenue FROM v ORDER BY month",
	     "columns": {"x": "month", "y_columns": "revenue"}}
	  ]
	}`)

	sp := creator.got.Spec
	if sp.Title != "Sales" {
		t.Errorf("title = %q — 'name' is what half the calls send", sp.Title)
	}
	if len(sp.Panels) != 1 {
		t.Fatalf("panels = %+v — 'cards' must still be read", sp.Panels)
	}
	p := sp.Panels[0]
	if p.Viz != spec.VizGroupedBar {
		t.Errorf("viz = %q, want the spaces and the case normalised away", p.Viz)
	}
	if p.SQL == "" {
		t.Error("'query' is a key models use for sql")
	}
	if len(p.Map.Series) != 1 || p.Map.Series[0] != "revenue" {
		t.Errorf("series = %v — a bare string means a one-series chart", p.Map.Series)
	}
	if p.Map.Label != "month" {
		t.Errorf("label = %q", p.Map.Label)
	}
}

// A model asked for grid coordinates produces overlapping ones, so a panel with
// no layout gets one that flows.
func TestCreateDashboardFlowsTheLayout(t *testing.T) {
	creator := &fakeCreator{}
	runTool(t, creator, `{
	  "title": "Ops",
	  "panels": [
	    {"viz": "bar", "sql": "SELECT a, b FROM v", "map": {"label": "a", "series": ["b"]}},
	    {"viz": "bar", "sql": "SELECT a, b FROM v", "map": {"label": "a", "series": ["b"]}},
	    {"viz": "bar", "sql": "SELECT a, b FROM v", "map": {"label": "a", "series": ["b"]}}
	  ]
	}`)

	panels := creator.got.Spec.Panels
	if len(panels) != 3 {
		t.Fatalf("panels = %d", len(panels))
	}
	for i, p := range panels {
		if p.Layout.W < 1 || p.Layout.H < 1 {
			t.Errorf("panel %d has no size: %+v", i, p.Layout)
		}
		if p.Layout.X+p.Layout.W > spec.GridColumns {
			t.Errorf("panel %d runs past the grid: %+v", i, p.Layout)
		}
	}
	// Two side by side, the third wrapped onto a new row.
	if panels[0].Layout.Y != panels[1].Layout.Y {
		t.Error("the first two panels should share a row")
	}
	if panels[2].Layout.Y == panels[0].Layout.Y {
		t.Error("the third panel should have wrapped to the next row")
	}
}

// A date_range with no default cannot resolve, and refusing the call teaches
// the model less than picking the window a business question usually means.
func TestCreateDashboardDefaultsADateRangeToAPreset(t *testing.T) {
	creator := &fakeCreator{}
	runTool(t, creator, `{
	  "title": "Ops",
	  "filters": [{"name": "period", "kind": "date_range"}],
	  "panels": [{"viz": "table", "sql": "SELECT a FROM v WHERE d >= {{period_from}} AND d < {{period_to}}"}]
	}`)

	f := creator.got.Spec.Filters[0]
	name, ok := f.Default.(string)
	if !ok || !spec.Preset(name).Valid() {
		t.Errorf("default = %v, want a preset name", f.Default)
	}
}

// The request the 2026-08-18 gate opened with: a dashboard about a quarter
// that has ended (T-D24). Both shapes a model writes it in reach the same
// stored window, and neither becomes a preset — `qtd` in a later year is the
// current quarter, where every panel returns nothing.
func TestCreateDashboardStoresAClosedWindowDefault(t *testing.T) {
	for name, filter := range map[string]string{
		"under default": `{"name": "period", "kind": "date_range", "default": {"from": "2024-10-01", "to": "2024-12-31"}}`,
		"on the filter": `{"name": "period", "kind": "date_range", "from": "2024-10-01", "to": "2024-12-31"}`,
	} {
		creator := &fakeCreator{}
		runTool(t, creator, `{
		  "title": "Q4 2024 Sales",
		  "filters": [`+filter+`],
		  "panels": [{"viz": "table", "sql": "SELECT a FROM v WHERE d >= {{period_from}} AND d < {{period_to}}"}]
		}`)

		def, ok := creator.got.Spec.Filters[0].Default.(map[string]any)
		if !ok {
			t.Errorf("%s: default = %#v, want the closed window", name, creator.got.Spec.Filters[0].Default)
			continue
		}
		if def["from"] != "2024-10-01" || def["to"] != "2024-12-31" {
			t.Errorf("%s: default = %#v", name, def)
		}
	}
}

func TestCreateDashboardRefusesWhatItCannotGuess(t *testing.T) {
	tool := NewCreateDashboardTool(&fakeCreator{}, nil, nil)
	for name, args := range map[string]string{
		"no title":  `{"panels": [{"viz": "table", "sql": "SELECT 1"}]}`,
		"no panels": `{"title": "Ops"}`,
		"empty":     `{"title": "Ops", "panels": []}`,
		"garbage":   `not json`,
	} {
		if _, err := tool.Execute(context.Background(), args); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}

// Warnings are the save path's whole point: a panel that did not answer must
// reach the model, or the reply says a dashboard is ready when a tile is blank.
func TestCreateDashboardReportsPanelWarnings(t *testing.T) {
	creator := &fakeCreator{res: &dashboard.SaveResult{
		Dashboard: &domain.Dashboard{ID: "dash-9"},
		Warnings:  []dashboard.PanelWarning{{PanelID: "p2", Message: "no rows"}},
	}}
	out, err := NewCreateDashboardTool(creator, nil, nil).
		Execute(context.Background(), `{"title":"X","panels":[{"viz":"table","sql":"SELECT 1"}]}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "p2") || !strings.Contains(out, "no rows") {
		t.Errorf("the payload hides the warning: %s", out)
	}
}

// A deployment with no dashboard service says so rather than panicking — the
// same shape every other optional dependency in this package takes.
func TestCreateDashboardWithoutAServiceSaysSo(t *testing.T) {
	_, err := NewCreateDashboardTool(nil, nil, nil).Execute(context.Background(), `{"title":"X","panels":[]}`)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("err = %v, want a 'not configured' refusal", err)
	}
}
