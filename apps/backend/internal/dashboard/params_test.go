package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/dashboard/spec"
)

func filtered(filters ...spec.Filter) *spec.Dashboard {
	return &spec.Dashboard{SpecVersion: spec.Version, Title: "d", SourceID: "s", TimeZone: "Asia/Jakarta", Filters: filters}
}

// 20:00 UTC on the 14th is already the 15th in Jakarta.
var now = time.Date(2024, 3, 14, 20, 0, 0, 0, time.UTC)

func day(t *testing.T, v any) string {
	t.Helper()
	ts, ok := v.(time.Time)
	if !ok {
		t.Fatalf("value is %T, want time.Time", v)
	}
	return ts.Format(DateLayout)
}

func TestBindFallsBackToTheStoredPreset(t *testing.T) {
	d := filtered(spec.Filter{Name: "period", Kind: spec.KindDateRange, Default: string(spec.PresetMTD)})
	p, err := Bind(d, nil, now)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got := day(t, p.Values["period_from"]); got != "2024-03-01" {
		t.Errorf("period_from = %s", got)
	}
	if got := day(t, p.Values["period_to"]); got != "2024-03-16" {
		t.Errorf("period_to = %s", got)
	}
	if p.Applied["period"] != "mtd" {
		t.Errorf("Applied should say which preset answered, got %q", p.Applied["period"])
	}
}

func TestBindPrefersAnExplicitRangeAndMakesItHalfOpen(t *testing.T) {
	d := filtered(spec.Filter{Name: "period", Kind: spec.KindDateRange, Default: string(spec.PresetMTD)})
	p, err := Bind(d, map[string]string{"period_from": "2024-01-01", "period_to": "2024-01-31"}, now)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	// The stated day is inclusive to a reader; the window is half-open. Without
	// the +1 day, "1–31 January" drops the 31st and every month is short.
	if got := day(t, p.Values["period_to"]); got != "2024-02-01" {
		t.Errorf("period_to = %s, want the day after the stated one", got)
	}
	if got := day(t, p.Values["period_from"]); got != "2024-01-01" {
		t.Errorf("period_from = %s", got)
	}
}

func TestBindRefusesHalfARange(t *testing.T) {
	d := filtered(spec.Filter{Name: "period", Kind: spec.KindDateRange, Default: string(spec.PresetMTD)})
	if _, err := Bind(d, map[string]string{"period_from": "2024-01-01"}, now); err == nil {
		t.Error("one bound is not a window — the other half would be a guess the reader cannot see")
	}
	if _, err := Bind(d, map[string]string{"period_from": "2024-02-01", "period_to": "2024-01-01"}, now); err == nil {
		t.Error("a backwards range must be refused")
	}
	if _, err := Bind(d, map[string]string{"period_from": "01/02/2024", "period_to": "2024-01-31"}, now); err == nil {
		t.Error("a date that is not YYYY-MM-DD must be refused")
	}
}

func TestBindCoercesTheOtherKinds(t *testing.T) {
	d := filtered(
		spec.Filter{Name: "channel", Kind: spec.KindEnum, Options: []string{"web", "store"}, Default: "web"},
		spec.Filter{Name: "floor", Kind: spec.KindNumber, Default: 0.0},
		spec.Filter{Name: "paid", Kind: spec.KindBool, Default: true},
		spec.Filter{Name: "asof", Kind: spec.KindDate, Default: "2024-01-31"},
	)
	p, err := Bind(d, map[string]string{"floor": "12.5", "paid": "false"}, now)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if p.Values["channel"] != "web" || p.Values["floor"] != 12.5 || p.Values["paid"] != false {
		t.Errorf("values = %#v", p.Values)
	}
	if got := day(t, p.Values["asof"]); got != "2024-01-31" {
		t.Errorf("asof = %s", got)
	}
	if _, err := Bind(d, map[string]string{"floor": "lots"}, now); err == nil {
		t.Error("a number filter must refuse a non-number")
	}
}

// Options are a UX affordance; the boundary is the bound parameter. A value
// outside the list returns no rows, which is the right answer — and a check here
// would suggest the check is what makes it safe.
func TestBindDoesNotEnforceEnumOptions(t *testing.T) {
	d := filtered(spec.Filter{Name: "channel", Kind: spec.KindEnum, Options: []string{"web"}, Default: "web"})
	p, err := Bind(d, map[string]string{"channel": "carrier-pigeon"}, now)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if p.Values["channel"] != "carrier-pigeon" {
		t.Errorf("value = %v", p.Values["channel"])
	}
}

// After T-D13 this map comes off a query string a stranger holding a share link
// can edit. A key the spec never declared is not a filter.
func TestBindIgnoresUndeclaredRequestKeys(t *testing.T) {
	d := filtered(spec.Filter{Name: "channel", Kind: spec.KindEnum, Default: "web"})
	p, err := Bind(d, map[string]string{"channel": "store", "tenant_id": "42", "refresh": "1"}, now)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if len(p.Values) != 1 || p.Values["channel"] != "store" {
		t.Errorf("values = %#v — only declared filters may bind", p.Values)
	}
}

func TestBindRefusesAFilterWithNeitherValueNorDefault(t *testing.T) {
	d := filtered(spec.Filter{Name: "channel", Kind: spec.KindEnum})
	_, err := Bind(d, nil, now)
	if err == nil {
		t.Fatal("a filter with no value and no default must be refused")
	}
	if !strings.Contains(err.Error(), "channel") {
		t.Errorf("the error should name the filter, got %q", err)
	}
}
