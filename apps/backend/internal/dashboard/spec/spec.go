// Package spec is the stored shape of a dashboard: what its panels ask, which
// columns they read, and which filters bind into them (T-D4).
//
// A panel stores a **question and a column mapping, never values**. That is the
// difference between a dashboard and a screenshot, and every rule in this
// package follows from it: the SQL carries {{tokens}} rather than dates, the
// mapping names columns rather than positions, and a default window is a preset
// name rather than two timestamps.
//
// The package is pure — types, validation and projection, no database and no
// clock — so the rules are testable without either. Binding and execution are
// the parent package's job.
package spec

import (
	"slices"
	"sort"
	"strings"

	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// Version is the spec version this release writes. It is stored on the row as
// well as in the JSON so a future reader can branch without parsing first.
const Version = 1

// SeriesCap is the most series a panel may draw.
//
// It is not a taste call and must not be re-derived elsewhere: it is the length
// of the chart palette in packages/design-tokens/tokens.json, and
// docs/coverage/report-charts.md records what a ninth series does — it wraps
// onto the first one's red, so two lines in one chart are the same colour and
// the chart is wrong in a way nobody can see.
const SeriesCap = 8

// Dashboard is the stored spec.
type Dashboard struct {
	SpecVersion int      `json:"spec_version"`
	Title       string   `json:"title"`
	SourceID    string   `json:"source_id"` // the default every panel inherits
	Filters     []Filter `json:"filters,omitempty"`
	Panels      []Panel  `json:"panels"`
	RefreshSecs int      `json:"refresh_secs,omitempty"`
	TimeZone    string   `json:"timezone,omitempty"`
}

// Panel is one tile: a question, where to draw it, and how to read the answer.
type Panel struct {
	ID     string `json:"id"` // stable across edits; the cache and the grid key on it
	Title  string `json:"title,omitempty"`
	Viz    Viz    `json:"viz"`
	Layout Layout `json:"layout"`

	// Exactly one of MetricKey and SQL is set. A kpi panel prefers MetricKey:
	// the registry's number is validated on save and re-checked on every read,
	// and a KPI the agent re-derived is the divergence 039_metric_definitions
	// exists to prevent — two threads, two revenues. Every other viz needs more
	// than one row, which the registry refuses by construction
	// (metric_service.go: maxRows 2), so it carries SQL.
	MetricKey string `json:"metric_key,omitempty"`
	SQL       string `json:"sql,omitempty"` // single SELECT, {{tokens}} bound at run time

	Map Mapping `json:"map"`
	Fmt Format  `json:"fmt,omitempty"`
}

// Mapping names the columns a panel reads.
//
// Named, never positional: SELECT * reorders the day the tenant adds a column,
// and a chart whose series silently became a different column draws without
// complaint and cannot be seen to be wrong.
type Mapping struct {
	Label      string   `json:"label,omitempty"`       // x-axis / category column
	Series     []string `json:"series,omitempty"`      // wide form: month, revenue, cost
	SeriesBy   string   `json:"series_by,omitempty"`   // long form: month, channel, revenue
	Value      string   `json:"value,omitempty"`       // the measure; kpi's number, long form's height
	DeltaValue string   `json:"delta_value,omitempty"` // kpi comparison column
}

// Layout places a panel on a 12-column grid in integer units.
type Layout struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// GridColumns is the width of the grid a Layout is placed on.
const GridColumns = 12

// Viz is how a panel draws its answer.
type Viz string

const (
	VizLine       Viz = "line"
	VizBar        Viz = "bar"
	VizGroupedBar Viz = "grouped_bar"
	VizStackedBar Viz = "stacked_bar"
	VizPie        Viz = "pie"
	VizDonut      Viz = "donut"
	VizKPI        Viz = "kpi"
	VizTable      Viz = "table"
)

// Vizzes lists every viz this release draws.
var Vizzes = []Viz{VizLine, VizBar, VizGroupedBar, VizStackedBar, VizPie, VizDonut, VizKPI, VizTable}

// Valid reports whether v is a viz this release draws.
func (v Viz) Valid() bool { return slices.Contains(Vizzes, v) }

// Categorical reports whether v draws one measure per category — the shape that
// cannot show a second series, so a spec that gives it one is a mistake worth
// naming rather than truncating.
func (v Viz) Categorical() bool { return v == VizPie || v == VizDonut }

// Format is how a number is written out. It is presentation only: the resolver
// returns raw values and the browser formats, so a CSV export and a chart label
// cannot disagree about the underlying number.
type Format string

const (
	FmtText     Format = "text"
	FmtNumber   Format = "number"
	FmtCurrency Format = "currency"
	FmtPercent  Format = "percent"
	FmtDate     Format = "date"
)

// Valid reports whether f is a format this release writes. An empty format is
// valid and means "the browser decides from the value".
func (f Format) Valid() bool {
	switch f {
	case "", FmtText, FmtNumber, FmtCurrency, FmtPercent, FmtDate:
		return true
	}
	return false
}

// Kind is a filter's type, which decides how a request value is coerced before
// it is bound.
type Kind string

const (
	KindDateRange Kind = "date_range"
	KindDate      Kind = "date"
	KindEnum      Kind = "enum"
	KindNumber    Kind = "number"
	KindBool      Kind = "bool"
)

// Kinds lists every filter kind this release coerces.
var Kinds = []Kind{KindDateRange, KindDate, KindEnum, KindNumber, KindBool}

// Valid reports whether k is a kind this release coerces.
func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// Filter declares a parameter the dashboard's panels may bind.
//
// Options are a UX affordance and are deliberately **not** enforced against the
// value a viewer sends. The security boundary is the bound parameter, not the
// option list: a value outside the set returns no rows, which is the correct
// outcome. A check here would suggest the check is what makes it safe, and the
// day somebody adds a kind without one, that belief is what breaks.
type Filter struct {
	Name    string   `json:"name"` // the {{token}} it binds; a date_range binds two
	Label   string   `json:"label,omitempty"`
	Kind    Kind     `json:"kind"`
	Options []string `json:"options,omitempty"` // enum affordance only
	Default any      `json:"default,omitempty"` // a date_range default is a PRESET NAME
}

// Tokens returns the {{tokens}} this filter binds.
//
// A date_range binds two — `name_from` and `name_to` — because a half-open
// window is two bounds and a panel writes them separately (`d >= {{p_from}} AND
// d < {{p_to}}`). The suffixes are a convention rather than a second pair of
// names on the filter, so there is one place a token can come from and the
// validator can list them all without asking the filter how it feels about it.
func (f Filter) Tokens() []sqlguard.Token {
	if f.Kind == KindDateRange {
		return []sqlguard.Token{sqlguard.Token(f.Name + "_from"), sqlguard.Token(f.Name + "_to")}
	}
	return []sqlguard.Token{sqlguard.Token(f.Name)}
}

// DeclaredTokens is every token the dashboard's filters bind, which is exactly
// the set a panel's SQL may reference.
func (d *Dashboard) DeclaredTokens() []sqlguard.Token {
	var out []sqlguard.Token
	for _, f := range d.Filters {
		out = append(out, f.Tokens()...)
	}
	return out
}

// Panel returns the panel with the given id.
func (d *Dashboard) Panel(id string) (*Panel, bool) {
	for i := range d.Panels {
		if d.Panels[i].ID == id {
			return &d.Panels[i], true
		}
	}
	return nil, false
}

// columnList renders result columns for an error message, the repair-
// instruction shape internal/tools/sql_error_hint.go uses: naming what would
// have worked is what makes the error actionable in one round-trip instead of
// two.
func columnList(cols []string) string {
	if len(cols) == 0 {
		return "the query returned no columns"
	}
	sorted := append([]string(nil), cols...)
	sort.Strings(sorted)
	return "the columns that would have worked are " + strings.Join(sorted, ", ")
}
