package spec

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Window is the [From, To) a panel is measured over. Half-open, matching
// metric.Window, so a template written for one reads correctly under the other.
type Window struct {
	From time.Time
	To   time.Time
}

// Preset is a named relative window, resolved server-side at request time.
//
// **A default is stored as a preset name, never as two timestamps.** That single
// rule is the whole difference between a live dashboard and a snapshot: two
// stored timestamps are correct on the day they were saved and wrong every day
// after, and nothing about the dashboard looks broken while it happens.
//
// The one thing that rule was wrong about is a dashboard whose subject is a
// period that has already ended, which cannot age into being wrong — see
// FixedWindow below (T-D24).
type Preset string

const (
	PresetLast7d    Preset = "last_7d"
	PresetLast30d   Preset = "last_30d"
	PresetMTD       Preset = "mtd"
	PresetQTD       Preset = "qtd"
	PresetYTD       Preset = "ytd"
	PresetLastMonth Preset = "last_month"
)

// Presets lists every preset this release resolves, in the order a filter's
// dropdown should offer them.
var Presets = []Preset{PresetLast7d, PresetLast30d, PresetMTD, PresetQTD, PresetYTD, PresetLastMonth}

// Valid reports whether p is a preset Resolve can answer.
func (p Preset) Valid() bool { return slices.Contains(Presets, p) }

// Resolve turns a preset into a concrete window in loc, as of now.
//
// Every window is day-aligned in the dashboard's own zone rather than a trailing
// multiple of 24 hours. A Jakarta retailer reading "last 7 days" at 09:00 means
// seven calendar days, not 168 hours ending mid-morning — and a trailing-hours
// window makes today's partial day move every time the panel refreshes, so the
// same chart redraws with a different first bucket while somebody watches it.
//
// The windows that include today run to the start of tomorrow, because To is
// exclusive and today's rows must be inside it. last_month is the one that does
// not: it is a closed calendar month, which is what somebody comparing months
// asks for.
func (p Preset) Resolve(now time.Time, loc *time.Location) (Window, error) {
	if loc == nil {
		loc = time.UTC
	}
	n := now.In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	tomorrow := today.AddDate(0, 0, 1)

	switch p {
	case PresetLast7d:
		return Window{From: today.AddDate(0, 0, -6), To: tomorrow}, nil
	case PresetLast30d:
		return Window{From: today.AddDate(0, 0, -29), To: tomorrow}, nil
	case PresetMTD:
		return Window{From: time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc), To: tomorrow}, nil
	case PresetQTD:
		firstMonthOfQuarter := time.Month((int(n.Month())-1)/3*3 + 1)
		return Window{From: time.Date(n.Year(), firstMonthOfQuarter, 1, 0, 0, 0, 0, loc), To: tomorrow}, nil
	case PresetYTD:
		return Window{From: time.Date(n.Year(), 1, 1, 0, 0, 0, 0, loc), To: tomorrow}, nil
	case PresetLastMonth:
		firstOfThisMonth := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
		return Window{From: firstOfThisMonth.AddDate(0, -1, 0), To: firstOfThisMonth}, nil
	default:
		return Window{}, fmt.Errorf("unknown window preset %q — the presets are %s", p, presetList())
	}
}

// dateLayout is how a stored window states a day, and it is the layout the
// parent package binds request values with (dashboard.DateLayout). One layout,
// no alternatives: a dashboard is read in a browser and shared by URL, and
// 03/04/2024 means two different days depending on who opens it.
//
// Unexported because this file is read by tygo: a Go time layout is not a wire
// type, and "2006-01-02" in a TypeScript module is a date nobody has.
const dateLayout = "2006-01-02"

// FixedWindow is a date_range default that names a period which has ended,
// instead of a preset that moves (T-D24).
//
// **Why this exists beside Preset, which the comment above says is the whole
// point.** That comment is right about a *live* dashboard and says nothing
// about one whose subject is a closed quarter — and "sales in Q4 2024" is an
// ordinary thing to build a dashboard about. Before this, the only vocabulary
// was a preset, so the 2026-08-18 gate's own request saved `qtd`, which in
// August 2026 is Q3 2026, where every panel returned nothing: the product
// stored something that draws an empty grid forever and told the user to fix
// the filter by hand on every open. `update_dashboard`'s tool description had
// been promising `{from, to}` since the day it shipped, and its parser built
// exactly this shape; the validator refused it, so the promise was dead on
// arrival.
//
// A fixed window does not age, because there is nothing left for it to age
// into. What it costs is that a dashboard saved with one is a report rather
// than a monitor, which is what the person who asked for Q4 2024 meant.
type FixedWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ParseFixedDefault reads a stored default as a fixed window.
//
// Three answers, and the middle one matters: (_, false, nil) means "this is
// not an object, so it is a preset name and not this function's business",
// while (_, true, err) means "it is an object and it does not state a window",
// which is a validation failure rather than a fall-through.
func ParseFixedDefault(def any) (FixedWindow, bool, error) {
	var raw map[string]any
	switch v := def.(type) {
	case FixedWindow:
		return v, true, v.validate()
	case map[string]any:
		raw = v
	case map[string]string:
		w := FixedWindow{From: strings.TrimSpace(v["from"]), To: strings.TrimSpace(v["to"])}
		return w, true, w.validate()
	default:
		return FixedWindow{}, false, nil
	}
	from, _ := raw["from"].(string)
	to, _ := raw["to"].(string)
	w := FixedWindow{From: strings.TrimSpace(from), To: strings.TrimSpace(to)}
	return w, true, w.validate()
}

// validate is what a stored fixed window must satisfy: both bounds, both
// readable as days, and in the order a reader wrote them.
func (w FixedWindow) validate() error {
	if w.From == "" || w.To == "" {
		return fmt.Errorf(`a fixed window needs both bounds, as {"from": "YYYY-MM-DD", "to": "YYYY-MM-DD"}`)
	}
	from, err := time.Parse(dateLayout, w.From)
	if err != nil {
		return fmt.Errorf("from %q is not a date as YYYY-MM-DD", w.From)
	}
	to, err := time.Parse(dateLayout, w.To)
	if err != nil {
		return fmt.Errorf("to %q is not a date as YYYY-MM-DD", w.To)
	}
	if to.Before(from) {
		return fmt.Errorf("to %s is before from %s", w.To, w.From)
	}
	return nil
}

// Resolve turns the two stated days into the half-open window a panel is
// measured over, in the dashboard's own zone.
//
// `to` is the day the reader named and is inclusive, so the window runs to the
// following midnight — the same convention Bind applies to an explicit
// from/to pair, and the reason "1–31 January" does not silently drop the 31st
// and report the month short by a day.
func (w FixedWindow) Resolve(loc *time.Location) (Window, error) {
	if err := w.validate(); err != nil {
		return Window{}, err
	}
	if loc == nil {
		loc = time.UTC
	}
	from, _ := time.ParseInLocation(dateLayout, w.From, loc)
	to, _ := time.ParseInLocation(dateLayout, w.To, loc)
	return Window{From: from, To: to.AddDate(0, 0, 1)}, nil
}

// String is what `applied_filters` shows for a fixed window: the two days,
// rather than a preset name the reader would go looking for in a list.
func (w FixedWindow) String() string { return w.From + "…" + w.To }

func presetList() string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = string(p)
	}
	return strings.Join(names, ", ")
}

// LoadLocation resolves a dashboard's stored time zone, falling back to UTC for
// an empty one.
//
// The zoneinfo database is embedded process-wide by internal/app's blank imports
// of time/tzdata (scheduled_task_service.go, watcher_service.go), so both
// cmd/api and cmd/worker can name a zone even though the deployed images carry
// no /usr/share/zoneinfo. An unknown zone is an error rather than a silent UTC:
// a dashboard whose windows quietly shifted seven hours is a support ticket
// nobody can reproduce.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}
	return loc, nil
}
