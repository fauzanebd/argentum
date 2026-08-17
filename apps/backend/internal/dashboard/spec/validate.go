package spec

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// The structural limits. Each one is a number somebody will want to raise, so
// each says what it is protecting rather than what it permits.
const (
	// TitleMax keeps a title inside a grid header and a share page's <title>.
	TitleMax = 200
	// PanelsMax bounds the fan-out one HTTP request can turn into against a
	// tenant's warehouse. The resolver runs four panels at a time, so this is the
	// number that decides the worst case a viewer waits through, not a storage
	// limit.
	PanelsMax = 24
	// FiltersMax bounds the control strip. Past this the dashboard has become a
	// query builder, which is out of scope for v1 and says so.
	FiltersMax = 8
	// PanelSQLMax matches the metric registry's template ceiling: long enough for
	// a real analytical query with CTEs, short enough that a paste accident is
	// refused at the door.
	PanelSQLMax = 20000
)

// identifierRe is the sqlguard token grammar. A filter name must match it,
// because the name becomes a {{token}} and a name the token regexp cannot match
// is a filter no panel can ever bind — a spec that saves and never works.
var identifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Validate checks everything about a spec that can be known without running it:
// structure, mappings, layout, and the tokens each panel's SQL references.
//
// It is deliberately all-or-nothing. Structure is the author's mistake and it is
// the same mistake every time the dashboard loads, so it is refused at save
// where somebody can read the message. Execution failures are the opposite —
// they depend on the data on the day — and T-D6 saves those with a warning
// instead. The split is the whole reason this function does not execute
// anything.
func Validate(d *Dashboard) error {
	if d == nil {
		return fmt.Errorf("the dashboard spec is missing")
	}
	// A zero version is a spec written before the field existed in the caller,
	// not an unknown one; the service stamps it. Anything ahead of us is a spec
	// this binary cannot be trusted to read.
	if d.SpecVersion > Version {
		return fmt.Errorf("spec_version %d is newer than this release understands (%d)", d.SpecVersion, Version)
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("the dashboard needs a title")
	}
	if len([]rune(d.Title)) > TitleMax {
		return fmt.Errorf("the title must be %d characters or fewer", TitleMax)
	}
	if d.SourceID == "" {
		return fmt.Errorf("the dashboard needs a source")
	}
	if d.RefreshSecs < 0 {
		return fmt.Errorf("refresh_secs cannot be negative")
	}
	if _, err := LoadLocation(d.TimeZone); err != nil {
		return err
	}
	if err := validateFilters(d.Filters); err != nil {
		return err
	}

	if len(d.Panels) == 0 {
		return fmt.Errorf("a dashboard needs at least one panel")
	}
	if len(d.Panels) > PanelsMax {
		return fmt.Errorf("a dashboard may hold %d panels; this one has %d", PanelsMax, len(d.Panels))
	}
	declared := d.DeclaredTokens()
	seenID := make(map[string]bool, len(d.Panels))
	for i := range d.Panels {
		p := &d.Panels[i]
		if p.ID == "" {
			return fmt.Errorf("panel %d has no id", i+1)
		}
		if seenID[p.ID] {
			return fmt.Errorf("two panels share the id %q — the grid and the cache key on it", p.ID)
		}
		seenID[p.ID] = true
		if err := validatePanel(p, declared); err != nil {
			return fmt.Errorf("panel %q: %w", p.ID, err)
		}
	}
	return nil
}

func validateFilters(filters []Filter) error {
	if len(filters) > FiltersMax {
		return fmt.Errorf("a dashboard may declare %d filters; this one declares %d", FiltersMax, len(filters))
	}
	// Tokens, not names, are what collide: a date_range named `p` and a plain
	// filter named `p_from` declare the same token and the second would silently
	// win at bind time.
	claimed := make(map[sqlguard.Token]string, len(filters)*2)
	for _, f := range filters {
		if !identifierRe.MatchString(f.Name) {
			return fmt.Errorf("filter name %q must be a letter or underscore followed by letters, digits or underscores — it becomes a {{token}}", f.Name)
		}
		if !f.Kind.Valid() {
			return fmt.Errorf("filter %q has kind %q; the kinds are %s", f.Name, f.Kind, kindList())
		}
		for _, tok := range f.Tokens() {
			if owner, dup := claimed[tok]; dup {
				return fmt.Errorf("filters %q and %q both declare {{%s}}", owner, f.Name, tok)
			}
			claimed[tok] = f.Name
		}
		if err := validateDefault(f); err != nil {
			return err
		}
	}
	return nil
}

// validateDefault checks only what a stored default must be, not what a request
// may send — coercing a request value is the parent package's job and happens
// per request.
func validateDefault(f Filter) error {
	if f.Default == nil {
		return nil
	}
	if f.Kind != KindDateRange {
		return nil
	}
	// The rule this whole ticket turns on: a date_range default is a PRESET NAME.
	// Two stored timestamps are correct on the day they were saved and wrong
	// every day after, and nothing about the dashboard looks broken while that
	// happens.
	//
	// **With one exception, added by T-D24: a window that has already closed.**
	// The rule above is about a dashboard that ages into being wrong, and a
	// quarter that ended cannot. Stated as an object rather than as a bare
	// string so the two shapes can never be confused — a preset is a name, a
	// fixed window is a pair of bounds — and refused by name when the object is
	// there and the window is not.
	if w, isFixed, err := ParseFixedDefault(f.Default); isFixed {
		if err != nil {
			return fmt.Errorf("filter %q: %w", f.Name, err)
		}
		_ = w
		return nil
	}
	name, ok := f.Default.(string)
	if !ok {
		return fmt.Errorf("filter %q: a date_range default must be a preset name (one of %s) "+
			`or a closed window as {"from": "YYYY-MM-DD", "to": "YYYY-MM-DD"}, not a stored timestamp`,
			f.Name, presetList())
	}
	if !Preset(name).Valid() {
		return fmt.Errorf("filter %q: unknown preset %q — the presets are %s", f.Name, name, presetList())
	}
	return nil
}

func validatePanel(p *Panel, declared []sqlguard.Token) error {
	if !p.Viz.Valid() {
		return fmt.Errorf("viz %q is not one this release draws (%s)", p.Viz, vizList())
	}
	if !p.Fmt.Valid() {
		return fmt.Errorf("fmt %q is not one this release writes", p.Fmt)
	}
	if err := validateLayout(p.Layout); err != nil {
		return err
	}

	// Decision 2: exactly one source, with the boundary at row cardinality.
	// Neither is a panel that asks nothing; both is two answers that can differ,
	// and the resolver would have to pick one silently.
	hasMetric, hasSQL := p.MetricKey != "", strings.TrimSpace(p.SQL) != ""
	switch {
	case hasMetric && hasSQL:
		return fmt.Errorf("a panel carries either metric_key or sql, never both")
	case !hasMetric && !hasSQL:
		return fmt.Errorf("a panel needs either metric_key or sql")
	case hasMetric && p.Viz != VizKPI:
		// The registry evaluates with maxRows 2 and refuses anything but one row
		// (metric_service.go), so a chart backed by a metric key could only ever
		// draw a single point. Naming that at save is kinder than a panel that
		// renders one dot forever.
		return fmt.Errorf("viz %q needs more than one row, which the metric registry refuses by construction — use sql", p.Viz)
	}

	if hasSQL {
		if len(p.SQL) > PanelSQLMax {
			return fmt.Errorf("the SQL is longer than %d characters", PanelSQLMax)
		}
		// No required tokens: a panel may bind every filter, some of them, or
		// none. What it may not do is reference a token no filter declares —
		// that renders to empty text, and `WHERE tenant = ` reads the whole table.
		if err := sqlguard.ValidateStatement(p.SQL, declared); err != nil {
			return err
		}
	}
	return validateMapping(p)
}

func validateLayout(l Layout) error {
	switch {
	case l.W < 1 || l.H < 1:
		return fmt.Errorf("layout w and h must be at least 1")
	case l.X < 0 || l.Y < 0:
		return fmt.Errorf("layout x and y cannot be negative")
	case l.X+l.W > GridColumns:
		return fmt.Errorf("layout runs past the %d-column grid (x %d + w %d)", GridColumns, l.X, l.W)
	}
	return nil
}

// validateMapping enforces what each viz needs to be drawable at all. A mapping
// that names the wrong column is caught at execution against the real columns;
// a mapping that names nothing cannot be drawn on any data and is refused here.
func validateMapping(p *Panel) error {
	m := p.Map

	// A metric-backed KPI needs no mapping: metric.Result already names its own
	// value, comparison, delta and unit, so there is nothing for an author to get
	// wrong and nothing for a tenant's column rename to break.
	if p.MetricKey != "" {
		return nil
	}

	if len(m.Series) > 0 && m.SeriesBy != "" {
		// Refused rather than resolved by precedence: wide and long form are two
		// different result shapes, and picking one silently draws a chart that is
		// wrong in a way the author cannot see.
		return fmt.Errorf("map sets both series and series_by — a result is wide or long, not both")
	}
	if len(m.Series) > SeriesCap {
		return fmt.Errorf("map names %d series; the palette is %d long", len(m.Series), SeriesCap)
	}

	switch p.Viz {
	case VizTable:
		// A table draws whatever the query returned. Naming columns would be a
		// second projection to keep in step with the SQL for no gain.
		return nil
	case VizKPI:
		if m.Value == "" {
			return fmt.Errorf("a sql-backed kpi needs map.value naming the column its number is in")
		}
		return nil
	case VizPie, VizDonut:
		if m.Label == "" || m.Value == "" {
			return fmt.Errorf("a %s needs map.label and map.value", p.Viz)
		}
		if len(m.Series) > 0 || m.SeriesBy != "" {
			return fmt.Errorf("a %s draws one measure per category and cannot show a second series", p.Viz)
		}
		return nil
	default: // line, bar, grouped_bar, stacked_bar
		if m.Label == "" {
			return fmt.Errorf("a %s needs map.label naming its category column", p.Viz)
		}
		if len(m.Series) == 0 && m.SeriesBy == "" {
			return fmt.Errorf("a %s needs map.series (wide) or map.series_by with map.value (long)", p.Viz)
		}
		if m.SeriesBy != "" && m.Value == "" {
			return fmt.Errorf("map.series_by needs map.value naming the measure column")
		}
		return nil
	}
}

func vizList() string {
	names := make([]string, len(Vizzes))
	for i, v := range Vizzes {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

func kindList() string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}
