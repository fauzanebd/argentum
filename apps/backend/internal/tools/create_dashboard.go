package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// DashboardCreator is the half of app.DashboardService this tool needs.
// Declared here rather than in internal/app to avoid an import cycle:
// internal/app already depends on internal/tools.
type DashboardCreator interface {
	Create(ctx context.Context, companyID, createdBy string, in dashboard.Input) (*dashboard.SaveResult, error)
}

// CreateDashboardTool builds a live dashboard from panels the agent describes
// in one call (T-D11).
//
// It replaces a pair — create_visualization then create_dashboard — that existed
// only because a Metabase card is a first-class object and a dashboard is a
// container for cards. Nothing native needs that round trip, and the pair had
// two costs beyond the extra calls. The first is the four-tool-calls-per-chart
// budget it spent. The second was a correctness bug the shape made necessary:
// create_dashboard could resolve "the cards made earlier in this conversation"
// out of a package-level map, which does not survive a worker restart and is
// wrong the moment there are two workers.
type CreateDashboardTool struct {
	svc      DashboardCreator
	recorder UsageRecorder
}

func NewCreateDashboardTool(svc DashboardCreator, recorder UsageRecorder) *CreateDashboardTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &CreateDashboardTool{svc: svc, recorder: recorder}
}

func (t *CreateDashboardTool) Name() string { return "create_dashboard" }

func (t *CreateDashboardTool) Description() string {
	return "Create a live dashboard from one or more panels and return a URL the user can open. " +
		"Each panel carries either a metric_key from the metric registry (preferred for single numbers — call list_metrics first) " +
		"or its own SQL, plus a chart type and which columns to plot. " +
		"The dashboard re-runs those queries every time somebody opens it, so it stays current without being rebuilt. " +
		"Call this ONCE with every panel the user asked for; there is no separate step for individual charts. " +
		"For an SQL panel, run its SQL with run_sql first and look at the column names it actually returns — 'map' must name " +
		"columns from that result, and a name the query does not produce is the most common way this call fails. " +
		"If an axis is time (date, month, week, quarter), the SQL MUST ORDER BY that column ascending so the chart reads left to right; " +
		"never rely on unspecified row order. " +
		"Add a 'filters' entry for anything the user should be able to change — a date range above all — and reference it in each " +
		"panel's SQL as {{period_from}} / {{period_to}} for a date_range named 'period', or {{your_filter_name}} for the others. " +
		"Those are bound as query parameters, so write them bare: WHERE created_at >= {{period_from}}, never quoted and never concatenated. " +
		"Returns dashboard_id, url, and per-panel warnings. Give the user the url as a markdown link with descriptive text, never the raw URL."
}

func (t *CreateDashboardTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"title": {
			Type:        "string",
			Description: "Title for the dashboard, in the user's own language",
			Required:    true,
		},
		"description": {
			Type:        "string",
			Description: "One sentence on what the dashboard answers",
			Required:    false,
		},
		"source_id": {
			Type:        "string",
			Description: "Which data source the panels read. Omit when the company has one source.",
			Required:    false,
		},
		"panels": {
			Type: "array",
			Description: "The panels, in reading order. Each: {title, viz, sql OR metric_key, map, fmt}. " +
				"viz is one of line, bar, grouped_bar, stacked_bar, pie, donut, kpi, table. " +
				"map names the columns: {label: 'month', series: ['revenue','cost']} for a wide result, " +
				"{label: 'month', series_by: 'channel', value: 'revenue'} for a long one, " +
				"{value: 'total', delta_value: 'previous'} for a kpi, and nothing at all for a table. " +
				"fmt is one of text, number, currency, percent, date. Layout is optional and flows automatically.",
			Required: true,
			Items: &interfaces.ParameterSpec{
				Type:        "object",
				Description: "One panel",
			},
		},
		"filters": {
			Type: "array",
			Description: "Controls the viewer can change. Each: {name, kind, label, options, default}. " +
				"kind is one of date_range, date, enum, number, bool. " +
				"A date_range named 'period' binds {{period_from}} and {{period_to}} in panel SQL, and its default is a " +
				"preset NAME — last_7d, last_30d, mtd, qtd, ytd or last_month — never a stored date, or the dashboard is " +
				"a snapshot that silently ages.",
			Required: false,
			Items: &interfaces.ParameterSpec{
				Type:        "object",
				Description: "One filter",
			},
		},
		"timezone": {
			Type:        "string",
			Description: "IANA zone the windows resolve in, e.g. Asia/Jakarta. Defaults to UTC.",
			Required:    false,
		},
	}
}

func (t *CreateDashboardTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *CreateDashboardTool) Execute(ctx context.Context, args string) (string, error) {
	if t.svc == nil {
		return "", fmt.Errorf("dashboards are not configured on this deployment")
	}
	logrus.Debugf("create_dashboard raw args: %s", args)

	in, err := parseDashboardArgs(args)
	if err != nil {
		return "", err
	}
	companyID := tenantctx.CompanyID(ctx)
	if threadID := tenantctx.ThreadID(ctx); threadID != "" {
		in.ThreadID = &threadID
	}

	res, err := t.svc.Create(ctx, companyID, "", *in)
	if err != nil {
		return "", err
	}
	t.recorder.RecordMetabaseDashboard(ctx, companyID, tenantctx.ThreadID(ctx))

	out := map[string]any{
		"dashboard_id": res.Dashboard.ID,
		"url":          "/dashboards/" + res.Dashboard.ID,
		"panel_count":  len(res.Dashboard.Spec.Panels),
		// row_count grounds the reply. guardrails.CheckFabrication reads
		// TurnEvidence.DataRows, and the metric registry's gate recorded what
		// happens when a data tool omits it: every answer built on that tool was
		// suppressed as a fabrication.
		"row_count": res.RowCount,
	}
	if len(res.Warnings) > 0 {
		out["warnings"] = res.Warnings
	}
	logrus.WithFields(logrus.Fields{
		"dashboard_id": res.Dashboard.ID,
		"panels":       len(res.Dashboard.Spec.Panels),
		"warnings":     len(res.Warnings),
	}).Info("created native dashboard")

	blob, _ := json.Marshal(out)
	return string(blob), nil
}

// parseDashboardArgs reads what a model actually sends rather than what the
// schema asks for.
//
// The tolerances are not politeness. Every one of them is a shape the previous
// pair was hit with in production or in the eval set: `cards` for `panels`,
// `name` for `title`, a bare string where an array belongs, a viz written with a
// space or a hyphen. Rejecting those costs a turn's iteration budget to teach
// the model something the parser can simply accept.
func parseDashboardArgs(args string) (*dashboard.Input, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse parameters: %w", err)
	}

	title := firstString(raw, "title", "name", "dashboard_title")
	if title == "" {
		return nil, fmt.Errorf("title parameter is required")
	}
	description := firstString(raw, "description", "subtitle")

	panelsRaw, ok := firstRaw(raw, "panels", "cards", "charts")
	if !ok {
		return nil, fmt.Errorf("panels parameter is required: an array of {title, viz, sql or metric_key, map}")
	}
	panels, err := parsePanels(panelsRaw)
	if err != nil {
		return nil, err
	}
	if len(panels) == 0 {
		return nil, fmt.Errorf("panels must not be empty")
	}

	var filters []spec.Filter
	if filtersRaw, ok := firstRaw(raw, "filters", "parameters"); ok {
		if filters, err = parseFilters(filtersRaw); err != nil {
			return nil, err
		}
	}

	sp := spec.Dashboard{
		SpecVersion: spec.Version,
		Title:       title,
		SourceID:    firstString(raw, "source_id", "sourceId", "source"),
		TimeZone:    firstString(raw, "timezone", "time_zone", "tz"),
		Filters:     filters,
		Panels:      panels,
	}
	return &dashboard.Input{Title: title, Description: description, Spec: sp}, nil
}

func parsePanels(raw json.RawMessage) ([]spec.Panel, error) {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("panels must be an array of objects: %w", err)
	}
	panels := make([]spec.Panel, 0, len(entries))
	// Flow the layout across a 12-column grid when the model does not state one.
	// A model asked for grid coordinates produces overlapping ones, and two
	// panels stacked on the same cell is a dashboard that looks broken for a
	// reason nobody can see in the JSON.
	col, row := 0, 0
	for i, e := range entries {
		p := spec.Panel{
			ID:        firstString(e, "id", "panel_id", "key"),
			Title:     firstString(e, "title", "name", "label"),
			Viz:       normaliseViz(firstString(e, "viz", "chart_type", "type", "visualization")),
			MetricKey: firstString(e, "metric_key", "metricKey", "metric"),
			SQL:       firstString(e, "sql", "query", "sql_query"),
			Fmt:       spec.Format(strings.ToLower(strings.TrimSpace(firstString(e, "fmt", "format")))),
		}
		if p.ID == "" {
			p.ID = fmt.Sprintf("panel-%d", i+1)
		}
		if p.Viz == "" {
			// A panel with SQL and no chart type is a table: it is the one viz
			// that needs no mapping, so it draws whatever the query returned
			// instead of failing on a guess about which column is the measure.
			p.Viz = spec.VizTable
			if p.MetricKey != "" {
				p.Viz = spec.VizKPI
			}
		}
		if mapRaw, ok := firstRaw(e, "map", "mapping", "columns"); ok {
			m, err := parseMapping(mapRaw)
			if err != nil {
				return nil, fmt.Errorf("panel %q: %w", p.ID, err)
			}
			p.Map = m
		}
		if layoutRaw, ok := firstRaw(e, "layout", "position"); ok {
			if err := json.Unmarshal(layoutRaw, &p.Layout); err != nil {
				return nil, fmt.Errorf("panel %q: layout must be {x, y, w, h}", p.ID)
			}
		}
		if p.Layout.W <= 0 || p.Layout.H <= 0 {
			p.Layout = flowLayout(p.Viz, &col, &row)
		}
		panels = append(panels, p)
	}
	return panels, nil
}

// flowLayout places the next panel left to right, wrapping at the grid's width.
// A KPI is a quarter wide and short because it holds one number; everything else
// is half a row, which is two charts side by side on a desktop and one per row
// once the grid collapses.
func flowLayout(viz spec.Viz, col, row *int) spec.Layout {
	w, h := 6, 4
	if viz == spec.VizKPI {
		w, h = 3, 2
	}
	if *col+w > spec.GridColumns {
		*col = 0
		*row += h
	}
	l := spec.Layout{X: *col, Y: *row, W: w, H: h}
	*col += w
	return l
}

func parseMapping(raw json.RawMessage) (spec.Mapping, error) {
	var e map[string]json.RawMessage
	if err := json.Unmarshal(raw, &e); err != nil {
		return spec.Mapping{}, fmt.Errorf("map must be an object naming columns")
	}
	m := spec.Mapping{
		Label:      firstString(e, "label", "x", "category", "dimension"),
		SeriesBy:   firstString(e, "series_by", "seriesBy", "group_by", "split_by"),
		Value:      firstString(e, "value", "y", "measure"),
		DeltaValue: firstString(e, "delta_value", "deltaValue", "comparison", "previous"),
	}
	if seriesRaw, ok := firstRaw(e, "series", "y_columns", "values"); ok {
		// A bare string is what a model sends for a one-series chart, and it
		// means the same thing as a one-element array.
		var one string
		if err := json.Unmarshal(seriesRaw, &one); err == nil {
			if one != "" {
				m.Series = []string{one}
			}
		} else if err := json.Unmarshal(seriesRaw, &m.Series); err != nil {
			return spec.Mapping{}, fmt.Errorf("map.series must be a column name or an array of them")
		}
	}
	return m, nil
}

func parseFilters(raw json.RawMessage) ([]spec.Filter, error) {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("filters must be an array of objects")
	}
	filters := make([]spec.Filter, 0, len(entries))
	for _, e := range entries {
		f := spec.Filter{
			Name:  firstString(e, "name", "key", "id"),
			Label: firstString(e, "label", "title"),
			Kind:  spec.Kind(normaliseToken(firstString(e, "kind", "type"))),
		}
		if f.Kind == "" {
			f.Kind = spec.KindDateRange
		}
		if optsRaw, ok := firstRaw(e, "options", "choices", "values"); ok {
			_ = json.Unmarshal(optsRaw, &f.Options) // an unreadable option list is a UX hint, not a failure
		}
		if defRaw, ok := firstRaw(e, "default", "default_value", "value"); ok {
			var v any
			if err := json.Unmarshal(defRaw, &v); err == nil {
				f.Default = v
			}
		}
		if f.Kind == spec.KindDateRange && f.Default == nil {
			// A range with no default cannot resolve, and refusing the call
			// teaches the model less than picking the window a business
			// question means nine times out of ten.
			f.Default = string(spec.PresetLast30d)
		}
		filters = append(filters, f)
	}
	return filters, nil
}

// normaliseViz accepts what models write — "grouped bar", "Stacked-Bar",
// "scalar" — and returns what the spec calls it.
func normaliseViz(v string) spec.Viz {
	switch normaliseToken(v) {
	case "":
		return ""
	case "scalar", "number", "metric", "kpi", "stat", "single_value":
		return spec.VizKPI
	case "line", "area", "timeseries", "time_series":
		return spec.VizLine
	case "bar", "column", "histogram":
		return spec.VizBar
	case "grouped_bar", "clustered_bar", "multi_bar":
		return spec.VizGroupedBar
	case "stacked_bar", "stacked_column":
		return spec.VizStackedBar
	case "pie":
		return spec.VizPie
	case "donut", "doughnut":
		return spec.VizDonut
	case "table", "grid", "list":
		return spec.VizTable
	default:
		// Unknown types fall through unchanged so spec.Validate names the
		// offender and lists what this release draws, rather than this function
		// silently choosing a chart the user did not ask for.
		return spec.Viz(normaliseToken(v))
	}
}

func normaliseToken(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "_")
	return strings.ReplaceAll(v, " ", "_")
}

// firstString returns the first key present that holds a string, tolerating a
// model that wraps a value in an object ({"text": "Sales"}) — a shape the
// previous tool met often enough to have a comment about it.
func firstString(raw map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		v, ok := raw[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return strings.TrimSpace(s)
		}
		var wrapped struct {
			Text  string `json:"text"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(v, &wrapped); err == nil {
			if wrapped.Text != "" {
				return strings.TrimSpace(wrapped.Text)
			}
			if wrapped.Value != "" {
				return strings.TrimSpace(wrapped.Value)
			}
		}
	}
	return ""
}

func firstRaw(raw map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, k := range keys {
		if v, ok := raw[k]; ok && len(v) > 0 && string(v) != "null" {
			return v, true
		}
	}
	return nil, false
}
