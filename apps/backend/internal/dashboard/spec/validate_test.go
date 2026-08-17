package spec

import (
	"strings"
	"testing"
)

// dash builds a minimal valid dashboard the tests then break in one place each,
// so every failure names the rule under test rather than the setup.
func dash(panels ...Panel) *Dashboard {
	return &Dashboard{
		SpecVersion: Version,
		Title:       "Revenue",
		SourceID:    "11111111-1111-1111-1111-111111111111",
		Filters: []Filter{
			{Name: "period", Kind: KindDateRange, Default: string(PresetLast30d)},
			{Name: "channel", Kind: KindEnum, Default: "all"},
		},
		Panels: panels,
	}
}

func barPanel() Panel {
	return Panel{
		ID:     "revenue-by-month",
		Viz:    VizBar,
		Layout: Layout{X: 0, Y: 0, W: 6, H: 4},
		SQL:    `SELECT month, revenue FROM v WHERE d >= {{period_from}} AND d < {{period_to}}`,
		Map:    Mapping{Label: "month", Series: []string{"revenue"}},
	}
}

func TestValidateAcceptsAWorkingDashboard(t *testing.T) {
	d := dash(barPanel(), Panel{
		ID:        "revenue-kpi",
		Viz:       VizKPI,
		Layout:    Layout{X: 6, Y: 0, W: 6, H: 2},
		MetricKey: "revenue",
	})
	if err := Validate(d); err != nil {
		t.Fatalf("Validate rejected a valid dashboard: %v", err)
	}
}

// Decision 2, enforced: exactly one source per panel.
func TestValidateRefusesBothOrNeitherSource(t *testing.T) {
	both := barPanel()
	both.MetricKey = "revenue"
	if err := Validate(dash(both)); err == nil {
		t.Error("a panel carrying metric_key AND sql must be refused")
	}

	neither := barPanel()
	neither.SQL = ""
	if err := Validate(dash(neither)); err == nil {
		t.Error("a panel carrying neither must be refused")
	}
}

// The registry evaluates with maxRows 2, so a metric-backed chart could only
// ever draw one point. Refused at save rather than discovered on the grid.
func TestValidateRefusesAMetricBackedChart(t *testing.T) {
	p := barPanel()
	p.SQL, p.MetricKey = "", "revenue"
	err := Validate(dash(p))
	if err == nil {
		t.Fatal("only a kpi may carry metric_key")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("the error should say why, got %q", err)
	}
}

func TestValidateRefusesWideAndLongTogether(t *testing.T) {
	p := barPanel()
	p.Map.SeriesBy = "channel"
	p.Map.Value = "revenue"
	if err := Validate(dash(p)); err == nil {
		t.Error("series and series_by together must be refused, not resolved by precedence")
	}
}

// The token rules the promotion to sqlguard made possible: a panel may bind some
// filters, none, or all — but never one nobody declared.
func TestValidateRefusesAnUndeclaredToken(t *testing.T) {
	p := barPanel()
	p.SQL = `SELECT month, revenue FROM v WHERE region = {{region}}`
	err := Validate(dash(p))
	if err == nil {
		t.Fatal("a token no filter declares must be refused")
	}
	if !strings.Contains(err.Error(), "region") {
		t.Errorf("the error should name the token, got %q", err)
	}

	none := barPanel()
	none.SQL = `SELECT month, revenue FROM v`
	if err := Validate(dash(none)); err != nil {
		t.Errorf("a panel that binds no filter must be allowed: %v", err)
	}
}

// A date_range named `p` and a plain filter named `p_from` declare the same
// token, and the second would silently win at bind time.
func TestValidateRefusesCollidingFilterTokens(t *testing.T) {
	d := dash(barPanel())
	d.Filters = append(d.Filters, Filter{Name: "period_from", Kind: KindDate, Default: "2024-01-01"})
	err := Validate(d)
	if err == nil {
		t.Fatal("two filters declaring one token must be refused")
	}
	if !strings.Contains(err.Error(), "period_from") {
		t.Errorf("the error should name the token, got %q", err)
	}
}

// The rule the whole ticket turns on: a stored default is a preset name, never
// two timestamps. Timestamps are correct on the day they are saved and wrong
// every day after, and nothing looks broken while it happens.
func TestValidateRefusesAStoredDateRangeDefault(t *testing.T) {
	d := dash(barPanel())
	d.Filters[0].Default = map[string]any{"from": "2024-01-01", "to": "2024-01-31"}
	err := Validate(d)
	if err == nil {
		t.Fatal("a date_range default must be a preset name")
	}
	if !strings.Contains(err.Error(), "preset") {
		t.Errorf("the error should say what a default must be, got %q", err)
	}

	d.Filters[0].Default = "last_fortnight"
	if err := Validate(d); err == nil {
		t.Error("an unknown preset must be refused")
	}
}

func TestValidateChecksTheGrid(t *testing.T) {
	wide := barPanel()
	wide.Layout = Layout{X: 8, W: 6, H: 2}
	if err := Validate(dash(wide)); err == nil {
		t.Error("a panel running past column 12 must be refused")
	}

	flat := barPanel()
	flat.Layout = Layout{X: 0, W: 6, H: 0}
	if err := Validate(dash(flat)); err == nil {
		t.Error("a zero-height panel must be refused")
	}
}

func TestValidateRefusesDuplicatePanelIDs(t *testing.T) {
	a, b := barPanel(), barPanel()
	err := Validate(dash(a, b))
	if err == nil {
		t.Fatal("two panels sharing an id must be refused — the cache keys on it")
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Errorf("the error should say why the id matters, got %q", err)
	}
}

func TestValidateChecksMappingsPerViz(t *testing.T) {
	kpi := Panel{ID: "k", Viz: VizKPI, Layout: Layout{W: 3, H: 2}, SQL: `SELECT total FROM v`}
	if err := Validate(dash(kpi)); err == nil {
		t.Error("a sql-backed kpi needs map.value")
	}
	kpi.Map.Value = "total"
	if err := Validate(dash(kpi)); err != nil {
		t.Errorf("a sql-backed kpi with map.value must pass: %v", err)
	}

	pie := Panel{ID: "p", Viz: VizPie, Layout: Layout{W: 4, H: 4}, SQL: `SELECT channel, revenue FROM v`,
		Map: Mapping{Label: "channel", Value: "revenue", SeriesBy: "region"}}
	if err := Validate(dash(pie)); err == nil {
		t.Error("a pie cannot carry a second series")
	}

	long := barPanel()
	long.Map = Mapping{Label: "month", SeriesBy: "channel"}
	if err := Validate(dash(long)); err == nil {
		t.Error("series_by without map.value must be refused")
	}

	table := Panel{ID: "t", Viz: VizTable, Layout: Layout{W: 12, H: 6}, SQL: `SELECT * FROM v`}
	if err := Validate(dash(table)); err != nil {
		t.Errorf("a table needs no mapping: %v", err)
	}
}

// A spec written by a newer release is refused rather than half-read: this
// binary cannot know what a field it does not have was load-bearing for.
func TestValidateRefusesANewerSpecVersion(t *testing.T) {
	d := dash(barPanel())
	d.SpecVersion = Version + 1
	if err := Validate(d); err == nil {
		t.Error("a newer spec_version must be refused")
	}
}

func TestValidateRefusesASQLPanelThatIsNotASingleSelect(t *testing.T) {
	p := barPanel()
	p.SQL = `SELECT month, revenue FROM v; DROP TABLE v`
	if err := Validate(dash(p)); err == nil {
		t.Error("sqlguard must run at save — a second statement is refused")
	}
}
