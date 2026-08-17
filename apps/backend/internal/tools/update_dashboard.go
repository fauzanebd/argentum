package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// DashboardReviser is the half of app.DashboardService this tool needs.
// Declared here rather than in internal/app for the same import-cycle reason
// DashboardCreator is: internal/app already depends on internal/tools.
type DashboardReviser interface {
	Get(ctx context.Context, companyID, id string) (*domain.Dashboard, error)
	List(ctx context.Context, companyID string) ([]*domain.Dashboard, error)
	Update(ctx context.Context, companyID, id string, in dashboard.Input) (*dashboard.SaveResult, error)
}

// recentDashboardsInAsk is how many dashboards the tool names when it has to ask
// which one. Five is enough for the model to recognise the one the user meant
// and short enough that the answer is a question rather than a list.
const recentDashboardsInAsk = 5

// UpdateDashboardTool revises a stored dashboard instead of building a second
// one (T-D22).
//
// The 2026-08-17 live gate ended with a dashboard whose default window matched
// no rows and a reply telling the user to change the filter by hand. The obvious
// next sentence from a customer — "just make it 2024" — had nowhere to land:
// DashboardService.Update was written, validated and dry-run gated, and reachable
// by nothing. The only fix available was another create_dashboard, which leaves
// the wrong dashboard in the list, breaks any link already sent, and pays the
// full build cost to change one date.
//
// Two decisions shape the parameters, and both are about what a model will
// actually send.
//
// It takes a PATCH, not a re-submission. A model re-emitting a twelve-panel spec
// to change one axis turns the cheapest edit in the product into the most
// expensive call in the registry, and every re-emission is a chance for a panel
// the user was happy with to come back subtly different.
//
// And a panel is addressed by TITLE first, index second. A model that counts
// panels gets it wrong the moment one is removed, and a title is what the user
// says out loud ("the pie chart"). A duplicate title is refused rather than
// resolved to the first match — silently editing one of two identically named
// panels is the failure nobody can see in the result.
type UpdateDashboardTool struct {
	svc      DashboardReviser
	repo     domain.ConnectionRepository
	recorder UsageRecorder
}

func NewUpdateDashboardTool(svc DashboardReviser, repo domain.ConnectionRepository, recorder UsageRecorder) *UpdateDashboardTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &UpdateDashboardTool{svc: svc, repo: repo, recorder: recorder}
}

func (t *UpdateDashboardTool) Name() string { return "update_dashboard" }

func (t *UpdateDashboardTool) Description() string {
	return "Change a dashboard that already exists, instead of building a second one. " +
		"PREFER this over create_dashboard whenever this conversation has already produced a dashboard and the user " +
		"is telling you what is wrong with it — a wider date range, a different chart type, one more panel, a better title. " +
		"Omit dashboard_id to edit the dashboard this conversation created; pass it to edit a specific one. " +
		"Send only what changes: 'panels' and 'filters' are lists of edits, each {op: add|replace|remove, ...}, " +
		"NOT the whole dashboard again. Address a panel by its title ('the revenue chart'), or by index if it has none. " +
		"A replace carries only the fields you are changing; everything you leave out keeps its current value. " +
		"You cannot change which data source a dashboard reads — that is a new dashboard, so call create_dashboard for it. " +
		"Returns dashboard_id, url and per-panel warnings; the id and the URL do not change, so a link already sent keeps working."
}

func (t *UpdateDashboardTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"dashboard_id": {
			Type: "string",
			Description: "Which dashboard to change. Omit to edit the one this conversation created — that is the " +
				"common case and the one to prefer.",
			Required: false,
		},
		"title": {
			Type:        "string",
			Description: "A new title, in the user's own language. Omit to keep the current one.",
			Required:    false,
		},
		"description": {
			Type:        "string",
			Description: "A new one-sentence description. Omit to keep the current one.",
			Required:    false,
		},
		"refresh_secs": {
			Type:        "number",
			Description: "How often an open dashboard re-runs its panels, in seconds. Omit to keep the current setting.",
			Required:    false,
		},
		"panels": {
			Type: "array",
			Description: "Panel edits, applied in order. Each: {op, title or index, ...the panel fields that change}. " +
				"op is add, replace or remove. " +
				"'title' names an existing panel case-insensitively; 'index' is its 0-based position and is the fallback " +
				"for a panel with no title. " +
				"A replace only needs the fields that change — {op: 'replace', title: 'Revenue', viz: 'line'} keeps that " +
				"panel's SQL, mapping and position. " +
				"An add takes the same fields create_dashboard's panels take and flows into the next free grid slot.",
			Required: false,
			Items: &interfaces.ParameterSpec{
				Type:        "object",
				Description: "One panel edit",
			},
		},
		"filters": {
			Type: "array",
			Description: "Filter edits, applied in order. Each: {op, name, kind, label, options, default}. " +
				"op is add, replace or remove; 'name' names the filter. " +
				"Changing a date_range's default is how you change the window the dashboard opens on — the default is a " +
				"preset NAME (last_7d, last_30d, mtd, qtd, ytd, last_month) or, for a fixed historical window the user " +
				"asked for by date, {from: 'YYYY-MM-DD', to: 'YYYY-MM-DD'}. " +
				"Removing or renaming a filter a panel's SQL still references is refused, so remove the {{token}} from " +
				"the SQL in the same call.",
			Required: false,
			Items: &interfaces.ParameterSpec{
				Type:        "object",
				Description: "One filter edit",
			},
		},
		"timezone": {
			Type:        "string",
			Description: "IANA zone the windows resolve in, e.g. Asia/Jakarta. Omit to keep the current one.",
			Required:    false,
		},
	}
}

func (t *UpdateDashboardTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *UpdateDashboardTool) Execute(ctx context.Context, args string) (string, error) {
	if t.svc == nil {
		return "", fmt.Errorf("dashboards are not configured on this deployment")
	}
	logrus.Debugf("update_dashboard raw args: %s", args)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return "", fmt.Errorf("failed to parse parameters: %w", err)
	}
	companyID := tenantctx.CompanyID(ctx)

	// Refuse a re-pointed source before anything else, and refuse it by name.
	// Changing which warehouse a stored dashboard reads is a different act with a
	// share-link blast radius: the same URL, already sent, starts serving another
	// database's numbers. It is a new dashboard, and saying so is what stops the
	// model retrying the edit with the id removed.
	if sourceID := firstString(raw, "source_id", "sourceId", "source"); sourceID != "" {
		return "", fmt.Errorf("update_dashboard cannot change which data source a dashboard reads; " +
			"a dashboard reading a different source is a new dashboard — call create_dashboard for it")
	}

	current, ask, err := t.resolve(ctx, companyID, firstString(raw, "dashboard_id", "dashboardId", "id"))
	if err != nil {
		return "", err
	}
	if ask != "" {
		// A result, not a Go error. The 2026-08-14 finding stands: a Go error in
		// answer to a caller mistake made deepseek re-send the identical call seven
		// times until the iteration budget ended the turn. A tool result the model
		// can read is what turns a dead end into a question for the user.
		return ask, nil
	}

	in, changed, err := applyDashboardEdits(current, raw)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", fmt.Errorf("nothing to change: pass a title, a refresh_secs, or at least one panel or filter edit")
	}

	// The same choke point every other data tool goes through (2026-08-17 defect
	// 1). A new tool that resolves its own source re-creates the hole that one
	// closed — and here it is a pure re-validation, because the source cannot
	// change: what it enforces is that the agent may still reach the source this
	// dashboard already reads.
	if t.repo != nil {
		source, err := ResolveSource(ctx, t.repo, companyID, current.SourceID)
		if err != nil {
			return "", err
		}
		in.Spec.SourceID = source.ID
	}

	// Hand the whole thing to the service so validation, the source-ownership
	// check and the zero-row dryRun warnings stay one code path. The zero-row
	// warning is the 2026-08-17 defect-2 fix, and an edit path that bypassed it
	// would put it back: the most likely edit in the product is a window change,
	// which is exactly the change that can match nothing.
	res, err := t.svc.Update(ctx, companyID, current.ID, *in)
	if err != nil {
		return "", err
	}
	t.recorder.RecordMetabaseDashboard(ctx, companyID, tenantctx.ThreadID(ctx))

	out := map[string]any{
		"dashboard_id": res.Dashboard.ID,
		"url":          "/dashboards/" + res.Dashboard.ID,
		"panel_count":  len(res.Dashboard.Spec.Panels),
		// Grounds the reply, the same way create_dashboard's does:
		// guardrails.CheckFabrication reads TurnEvidence.DataRows, and a tool that
		// returns a URL and no row count gets every answer built on it suppressed
		// as a fabrication.
		"row_count": res.RowCount,
		"updated":   true,
	}
	if len(res.Warnings) > 0 {
		out["warnings"] = res.Warnings
	}
	// TODO(T-D13): when share tokens exist, an edit silently changes what a
	// stranger's link serves. The tool result has to name the number of live
	// shares at that point, so the model can say "this is shared with 3 people"
	// rather than changing it under them.
	logrus.WithFields(logrus.Fields{
		"dashboard_id": res.Dashboard.ID,
		"panels":       len(res.Dashboard.Spec.Panels),
		"warnings":     len(res.Warnings),
	}).Info("updated native dashboard")

	blob, _ := json.Marshal(out)
	return string(blob), nil
}

// resolve picks the dashboard this call edits.
//
// Returns exactly one of: the dashboard, or an `ask` payload naming the recent
// ones. The second is a tool RESULT rather than an error — see the call site.
func (t *UpdateDashboardTool) resolve(ctx context.Context, companyID, id string) (*domain.Dashboard, string, error) {
	if id != "" {
		d, err := t.svc.Get(ctx, companyID, id)
		if err != nil {
			return nil, "", err
		}
		return d, "", nil
	}

	all, err := t.svc.List(ctx, companyID)
	if err != nil {
		return nil, "", err
	}
	// The newest dashboard this thread produced. thread_id is a real, indexed
	// column (056) — deliberately not the package-level in-memory map the old
	// create_visualization pair used, which did not survive a worker restart and
	// was wrong the moment there were two workers.
	if threadID := tenantctx.ThreadID(ctx); threadID != "" {
		for _, d := range all { // ListByCompany is created_at DESC, so the first match is the newest
			if d.ThreadID != nil && *d.ThreadID == threadID {
				return d, "", nil
			}
		}
	}

	recent := make([]map[string]any, 0, recentDashboardsInAsk)
	for _, d := range all {
		if len(recent) == recentDashboardsInAsk {
			break
		}
		recent = append(recent, map[string]any{
			"dashboard_id": d.ID,
			"title":        d.Title,
			"created_at":   d.CreatedAt,
		})
	}
	msg := "This conversation has not created a dashboard, so there is nothing to edit by default. " +
		"Ask the user which of these they mean and call update_dashboard again with its dashboard_id."
	if len(recent) == 0 {
		msg = "This workspace has no dashboards yet. Nothing to edit — build one with create_dashboard."
	}
	blob, _ := json.Marshal(map[string]any{
		"needs_dashboard_id": true,
		"recent_dashboards":  recent,
		// Zero, and stated rather than omitted: nothing was read from the
		// warehouse, so a reply that quotes a figure on the back of this call is a
		// fabrication and the guardrail should say so.
		"row_count": 0,
		"message":   msg,
	})
	return nil, string(blob), nil
}

// applyDashboardEdits merges a patch onto the stored spec.
//
// It reports whether anything actually changed, so a call that named no edit is
// refused instead of writing an identical row and reporting success — the shape
// a model falls into when it half-understands a tool, and the one that reads to
// the user as "it says it fixed it and nothing moved".
func applyDashboardEdits(current *domain.Dashboard, raw map[string]json.RawMessage) (*dashboard.Input, bool, error) {
	sp := current.Spec
	sp.Panels = append([]spec.Panel(nil), current.Spec.Panels...)
	sp.Filters = append([]spec.Filter(nil), current.Spec.Filters...)
	if sp.SpecVersion == 0 {
		sp.SpecVersion = spec.Version
	}
	sp.SourceID = current.SourceID

	changed := false
	title, description := current.Title, current.Description
	if v := firstString(raw, "title", "name", "dashboard_title"); v != "" && v != title {
		title, sp.Title, changed = v, v, true
	}
	if v := firstString(raw, "description", "subtitle"); v != "" && v != description {
		description, changed = v, true
	}
	if v := firstString(raw, "timezone", "time_zone", "tz"); v != "" && v != sp.TimeZone {
		sp.TimeZone, changed = v, true
	}
	if v, ok := firstRaw(raw, "refresh_secs", "refreshSecs", "refresh"); ok {
		var secs int
		if err := json.Unmarshal(v, &secs); err != nil {
			return nil, false, fmt.Errorf("refresh_secs must be a number of seconds")
		}
		if secs != sp.RefreshSecs {
			sp.RefreshSecs, changed = secs, true
		}
	}

	if v, ok := firstRaw(raw, "panels", "cards", "charts", "panel_edits"); ok {
		n, err := applyPanelEdits(&sp, v)
		if err != nil {
			return nil, false, err
		}
		changed = changed || n > 0
	}
	if v, ok := firstRaw(raw, "filters", "parameters", "filter_edits"); ok {
		n, err := applyFilterEdits(&sp, v)
		if err != nil {
			return nil, false, err
		}
		changed = changed || n > 0
	}

	// ThreadID is left nil on purpose: DashboardService.validated carries the
	// stored one forward when the input omits it, so editing a dashboard from a
	// different thread does not re-attribute which conversation produced it.
	return &dashboard.Input{Title: title, Description: description, Spec: sp}, changed, nil
}

func applyPanelEdits(sp *spec.Dashboard, raw json.RawMessage) (int, error) {
	entries, err := editEntries(raw, "panels")
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, e := range entries {
		op := editOp(e)
		switch op {
		case "add":
			p, err := panelFromEntry(e, spec.Panel{}, len(sp.Panels))
			if err != nil {
				return 0, err
			}
			// Place it after everything already on the grid rather than flowing from
			// the origin: a new panel that lands on top of an existing one is a
			// dashboard that looks broken for a reason nobody can see in the JSON.
			col, row := 0, nextFreeRow(sp.Panels)
			p.Layout = flowLayout(p.Viz, &col, &row)
			sp.Panels = append(sp.Panels, p)
		case "replace":
			i, err := findPanel(sp.Panels, e)
			if err != nil {
				return 0, err
			}
			p, err := panelFromEntry(e, sp.Panels[i], i)
			if err != nil {
				return 0, err
			}
			sp.Panels[i] = p
		case "remove":
			i, err := findPanel(sp.Panels, e)
			if err != nil {
				return 0, err
			}
			sp.Panels = append(sp.Panels[:i], sp.Panels[i+1:]...)
		default:
			return 0, fmt.Errorf("panel edit %d: op must be add, replace or remove (got %q)", applied+1, op)
		}
		applied++
	}
	return applied, nil
}

func applyFilterEdits(sp *spec.Dashboard, raw json.RawMessage) (int, error) {
	entries, err := editEntries(raw, "filters")
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, e := range entries {
		op := editOp(e)
		name := firstString(e, "name", "key", "id", "filter")
		if name == "" {
			return 0, fmt.Errorf("filter edit %d: name is required — it is the {{token}} the panels bind", applied+1)
		}
		i := indexOfFilter(sp.Filters, name)
		switch op {
		case "add":
			if i < 0 {
				f, err := filterFromEntry(e, spec.Filter{Name: name})
				if err != nil {
					return 0, err
				}
				sp.Filters = append(sp.Filters, f)
				applied++
				continue
			}
			// An add for a filter that already exists is what a model sends when it
			// means "make the window 2024". Falling through to replace is the
			// reading that matches the intent; refusing it costs a round trip to
			// teach the model a word.
			fallthrough
		case "replace":
			if i < 0 {
				return 0, fmt.Errorf("no filter named %q; this dashboard has %s", name, filterList(sp.Filters))
			}
			f, err := filterFromEntry(e, sp.Filters[i])
			if err != nil {
				return 0, err
			}
			sp.Filters[i] = f
		case "remove":
			if i < 0 {
				return 0, fmt.Errorf("no filter named %q; this dashboard has %s", name, filterList(sp.Filters))
			}
			sp.Filters = append(sp.Filters[:i], sp.Filters[i+1:]...)
		default:
			return 0, fmt.Errorf("filter edit %d: op must be add, replace or remove (got %q)", applied+1, op)
		}
		applied++
	}
	return applied, nil
}

// editEntries reads a list of edits, tolerating the one-edit-not-in-an-array
// shape a model sends when it is changing a single thing.
func editEntries(raw json.RawMessage, field string) ([]map[string]json.RawMessage, error) {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries, nil
	}
	var one map[string]json.RawMessage
	if err := json.Unmarshal(raw, &one); err == nil {
		return []map[string]json.RawMessage{one}, nil
	}
	return nil, fmt.Errorf("%s must be a list of edits, each {op, title or name, ...}", field)
}

// editOp defaults to replace, because that is what a model means when it names a
// panel and some fields and no operation. An add is the case it states; a remove
// it always states.
func editOp(e map[string]json.RawMessage) string {
	op := normaliseToken(firstString(e, "op", "operation", "action"))
	switch op {
	case "", "update", "edit", "change", "modify", "set":
		return "replace"
	case "insert", "append", "create", "new":
		return "add"
	case "delete", "drop", "rm":
		return "remove"
	}
	return op
}

// findPanel resolves an edit's target: title first, index second.
//
// A duplicate title is refused rather than resolved to the first match. The
// alternative silently edits one of two identically named panels, and the result
// looks correct in the tool's reply and wrong on the grid.
func findPanel(panels []spec.Panel, e map[string]json.RawMessage) (int, error) {
	if id := firstString(e, "panel_id", "panelId"); id != "" {
		for i := range panels {
			if panels[i].ID == id {
				return i, nil
			}
		}
		return -1, fmt.Errorf("no panel with id %q; this dashboard has %s", id, panelList(panels))
	}
	if title := firstString(e, "title", "panel", "name", "label"); title != "" {
		want := strings.ToLower(strings.TrimSpace(title))
		found := -1
		for i := range panels {
			if strings.ToLower(strings.TrimSpace(panels[i].Title)) != want {
				continue
			}
			if found >= 0 {
				return -1, fmt.Errorf("two panels are called %q, so naming one is ambiguous — "+
					"address them by index (0-based) instead", title)
			}
			found = i
		}
		if found >= 0 {
			return found, nil
		}
		return -1, fmt.Errorf("no panel called %q; this dashboard has %s", title, panelList(panels))
	}
	if v, ok := firstRaw(e, "index", "position", "panel_index"); ok {
		var i int
		if err := json.Unmarshal(v, &i); err != nil {
			return -1, fmt.Errorf("index must be a 0-based number")
		}
		if i < 0 || i >= len(panels) {
			return -1, fmt.Errorf("index %d is outside this dashboard's %d panels", i, len(panels))
		}
		return i, nil
	}
	return -1, fmt.Errorf("name which panel to change: a title, or an index if it has none — %s", panelList(panels))
}

// panelFromEntry overlays the fields an edit names onto a base panel.
//
// Everything unnamed keeps its current value, which is the whole point of a
// patch: the model changes an axis without re-emitting the SQL, and the SQL that
// was working cannot come back subtly different.
func panelFromEntry(e map[string]json.RawMessage, base spec.Panel, index int) (spec.Panel, error) {
	p := base
	if v := firstString(e, "new_title", "rename_to"); v != "" {
		p.Title = v
	} else if v := firstString(e, "title", "name", "label"); v != "" && base.Title == "" {
		// On an add, `title` is the panel's title. On a replace it is the address,
		// and renaming goes through new_title — otherwise a replace that names the
		// panel it is editing would be indistinguishable from one renaming it.
		p.Title = v
	}
	if v := normaliseViz(firstString(e, "viz", "chart_type", "type", "visualization")); v != "" {
		p.Viz = v
	}
	if v := firstString(e, "sql", "query", "sql_query"); v != "" {
		p.SQL = v
		// A panel carries EXACTLY one of metric_key and SQL — spec.Validate refuses
		// both — so supplying one clears the other rather than producing a spec the
		// validator rejects with a message about a field the model never sent.
		p.MetricKey = ""
	}
	if v := firstString(e, "metric_key", "metricKey", "metric"); v != "" {
		p.MetricKey = v
		p.SQL = ""
	}
	if v := firstString(e, "fmt", "format"); v != "" {
		p.Fmt = spec.Format(normaliseToken(v))
	}
	if mapRaw, ok := firstRaw(e, "map", "mapping", "columns"); ok {
		m, err := parseMapping(mapRaw)
		if err != nil {
			return p, fmt.Errorf("panel %q: %w", addressOf(e, index), err)
		}
		p.Map = m
	}
	if layoutRaw, ok := firstRaw(e, "layout", "position"); ok {
		if err := json.Unmarshal(layoutRaw, &p.Layout); err != nil {
			return p, fmt.Errorf("panel %q: layout must be {x, y, w, h}", addressOf(e, index))
		}
	}
	if p.ID == "" {
		p.ID = fmt.Sprintf("panel-%d", index+1)
	}
	if p.Viz == "" {
		p.Viz = spec.VizTable
		if p.MetricKey != "" {
			p.Viz = spec.VizKPI
		}
	}
	// A viz change alone can invalidate the mapping the panel already had — a
	// table has none, a kpi wants a value. spec.Validate is what says so, with the
	// columns that would have worked, and it runs on the merged spec.
	return p, nil
}

func filterFromEntry(e map[string]json.RawMessage, base spec.Filter) (spec.Filter, error) {
	f := base
	if v := firstString(e, "label", "title"); v != "" {
		f.Label = v
	}
	if v := normaliseToken(firstString(e, "kind", "type")); v != "" {
		f.Kind = spec.Kind(v)
	}
	if f.Kind == "" {
		f.Kind = spec.KindDateRange
	}
	if optsRaw, ok := firstRaw(e, "options", "choices", "values"); ok {
		var opts []string
		if err := json.Unmarshal(optsRaw, &opts); err == nil {
			f.Options = opts
		}
	}
	if defRaw, ok := firstRaw(e, "default", "default_value", "value", "from", "preset"); ok {
		// A two-date window arrives either as {from, to} on the edit itself or as a
		// {from, to} object under `default`, depending on the model. Both are the
		// same request and both are accepted; a preset name stays a string, because
		// a stored preset is what keeps the dashboard live rather than a snapshot.
		if from := firstString(e, "from", "start", "date_from"); from != "" {
			to := firstString(e, "to", "end", "date_to")
			f.Default = map[string]any{"from": from, "to": to}
		} else {
			var v any
			if err := json.Unmarshal(defRaw, &v); err == nil {
				f.Default = v
			}
		}
	}
	if f.Kind == spec.KindDateRange && f.Default == nil {
		f.Default = string(spec.PresetLast30d)
	}
	return f, nil
}

// nextFreeRow is the first grid row below everything already placed.
func nextFreeRow(panels []spec.Panel) int {
	row := 0
	for _, p := range panels {
		if bottom := p.Layout.Y + p.Layout.H; bottom > row {
			row = bottom
		}
	}
	return row
}

func indexOfFilter(filters []spec.Filter, name string) int {
	want := strings.ToLower(strings.TrimSpace(name))
	for i := range filters {
		if strings.ToLower(strings.TrimSpace(filters[i].Name)) == want {
			return i
		}
	}
	return -1
}

// addressOf names a panel the way the edit did, for an error the model can act
// on without re-reading its own call.
func addressOf(e map[string]json.RawMessage, index int) string {
	if t := firstString(e, "title", "name", "label"); t != "" {
		return t
	}
	return fmt.Sprintf("index %d", index)
}

// panelList and filterList name what would have worked. Same repair-instruction
// shape internal/tools/sql_error_hint.go uses for a bad column: an error that
// lists the alternatives is answered in one round trip instead of three.
func panelList(panels []spec.Panel) string {
	if len(panels) == 0 {
		return "no panels at all"
	}
	names := make([]string, 0, len(panels))
	for i, p := range panels {
		if strings.TrimSpace(p.Title) == "" {
			names = append(names, fmt.Sprintf("index %d (untitled %s)", i, p.Viz))
			continue
		}
		names = append(names, fmt.Sprintf("%q (index %d)", p.Title, i))
	}
	return strings.Join(names, ", ")
}

func filterList(filters []spec.Filter) string {
	if len(filters) == 0 {
		return "no filters at all"
	}
	names := make([]string, 0, len(filters))
	for _, f := range filters {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
