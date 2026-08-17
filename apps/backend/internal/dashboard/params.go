package dashboard

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/dashboard/spec"
)

// DateLayout is how a filter value states a day. One layout, no alternatives:
// a dashboard is read in a browser and shared by URL, and 03/04/2024 means two
// different days depending on who opens it.
const DateLayout = "2006-01-02"

// Params is one request's filter state: the values panels bind, plus what the
// viewer effectively chose, so the response can say which window it answered
// over rather than leaving the reader to assume.
type Params struct {
	// Values is keyed by {{token}} name — a date_range named `period` fills
	// `period_from` and `period_to`.
	Values map[string]any
	// Windows is the resolved [From, To) per date_range filter.
	Windows map[string]spec.Window
	// Applied echoes the choice per filter: a preset name, an explicit range, or
	// the value itself.
	Applied map[string]string
}

// Bind coerces a request's raw filter values against the dashboard's declared
// filters, falling back to each filter's default.
//
// `now` and the dashboard's time zone are what make a stored preset a live
// window, so both are arguments rather than reads — the same reason
// metric.ValidationWindow takes a clock.
//
// Request keys are token names: `period` for a date_range's preset, or
// `period_from` and `period_to` for an explicit range. Anything the dashboard
// does not declare is **ignored, never merged** — after T-D13 this map comes
// off a query string a stranger holding a share link can edit, and a filter the
// spec did not declare is not a filter.
func Bind(d *spec.Dashboard, req map[string]string, now time.Time) (*Params, error) {
	loc, err := spec.LoadLocation(d.TimeZone)
	if err != nil {
		return nil, err
	}
	p := &Params{
		Values:  make(map[string]any, len(d.Filters)*2),
		Windows: make(map[string]spec.Window),
		Applied: make(map[string]string, len(d.Filters)),
	}
	for _, f := range d.Filters {
		if err := bindFilter(p, f, req, now, loc); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func bindFilter(p *Params, f spec.Filter, req map[string]string, now time.Time, loc *time.Location) error {
	if f.Kind == spec.KindDateRange {
		return bindDateRange(p, f, req, now, loc)
	}

	raw, ok := trimmed(req, f.Name)
	if !ok {
		if f.Default == nil {
			return fmt.Errorf("filter %q needs a value and the dashboard declares no default", f.Name)
		}
		v, err := coerceDefault(f, loc)
		if err != nil {
			return err
		}
		p.Values[f.Name] = v
		p.Applied[f.Name] = fmt.Sprintf("%v", f.Default)
		return nil
	}

	v, err := coerce(f, raw, loc)
	if err != nil {
		return err
	}
	p.Values[f.Name] = v
	p.Applied[f.Name] = raw
	return nil
}

// bindDateRange resolves the two bounds a range binds, preferring an explicit
// from/to pair, then a preset, then the stored default preset.
func bindDateRange(p *Params, f spec.Filter, req map[string]string, now time.Time, loc *time.Location) error {
	fromKey, toKey := f.Name+"_from", f.Name+"_to"
	rawFrom, hasFrom := trimmed(req, fromKey)
	rawTo, hasTo := trimmed(req, toKey)

	switch {
	case hasFrom != hasTo:
		// One bound is not a window. Refused rather than half-applied, because
		// the half that would be filled in is a guess the reader cannot see.
		return fmt.Errorf("filter %q needs both %s and %s, or neither", f.Name, fromKey, toKey)

	case hasFrom && hasTo:
		from, err := parseDay(rawFrom, loc)
		if err != nil {
			return fmt.Errorf("filter %q: %s %w", f.Name, fromKey, err)
		}
		to, err := parseDay(rawTo, loc)
		if err != nil {
			return fmt.Errorf("filter %q: %s %w", f.Name, toKey, err)
		}
		if !to.After(from) {
			return fmt.Errorf("filter %q: %s must be after %s", f.Name, toKey, fromKey)
		}
		// The stated day is inclusive to a reader and the window is half-open, so
		// `to` becomes the following midnight. Without this, "1–31 January"
		// silently drops the 31st and the total is wrong by a day every month.
		return applyWindow(p, f, spec.Window{From: from, To: to.AddDate(0, 0, 1)}, rawFrom+"…"+rawTo)

	default:
		name, ok := trimmed(req, f.Name)
		if !ok {
			def, isString := f.Default.(string)
			if !isString || def == "" {
				return fmt.Errorf("filter %q needs a window and the dashboard declares no default preset", f.Name)
			}
			name = def
		}
		w, err := spec.Preset(name).Resolve(now, loc)
		if err != nil {
			return fmt.Errorf("filter %q: %w", f.Name, err)
		}
		return applyWindow(p, f, w, name)
	}
}

func applyWindow(p *Params, f spec.Filter, w spec.Window, applied string) error {
	p.Values[f.Name+"_from"] = w.From
	p.Values[f.Name+"_to"] = w.To
	p.Windows[f.Name] = w
	p.Applied[f.Name] = applied
	return nil
}

// coerce turns one request string into the Go value the driver binds.
//
// An enum's value is deliberately not checked against its option list. Options
// are a UX affordance; the security boundary is the bound parameter shipped in
// T-D1. A value outside the set returns no rows, which is the correct outcome —
// and a check here would suggest the check is what makes it safe.
func coerce(f spec.Filter, raw string, loc *time.Location) (any, error) {
	switch f.Kind {
	case spec.KindDate:
		d, err := parseDay(raw, loc)
		if err != nil {
			return nil, fmt.Errorf("filter %q %w", f.Name, err)
		}
		return d, nil
	case spec.KindEnum:
		return raw, nil
	case spec.KindNumber:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("filter %q wants a number, got %q", f.Name, raw)
		}
		return n, nil
	case spec.KindBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("filter %q wants true or false, got %q", f.Name, raw)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("filter %q has kind %q, which this release cannot bind", f.Name, f.Kind)
	}
}

// coerceDefault runs a stored default through the same coercion a request value
// gets, so a default that would be refused from a viewer is refused at save
// rather than binding something a request never could.
func coerceDefault(f spec.Filter, loc *time.Location) (any, error) {
	switch v := f.Default.(type) {
	case string:
		return coerce(f, v, loc)
	case float64: // every number that arrives through JSON
		if f.Kind == spec.KindNumber {
			return v, nil
		}
	case bool:
		if f.Kind == spec.KindBool {
			return v, nil
		}
	}
	return nil, fmt.Errorf("filter %q has a default of type %T, which its kind %q cannot bind", f.Name, f.Default, f.Kind)
}

func parseDay(raw string, loc *time.Location) (time.Time, error) {
	d, err := time.ParseInLocation(DateLayout, raw, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("wants a date as YYYY-MM-DD, got %q", raw)
	}
	return d, nil
}

func trimmed(req map[string]string, key string) (string, bool) {
	v, ok := req[key]
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}
