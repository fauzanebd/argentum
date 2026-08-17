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
