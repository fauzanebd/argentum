package spec

import (
	"strings"
	"testing"
	"time"
)

// Jakarta, because the tenant this product was built for is there and because a
// +07:00 zone catches a UTC-day boundary bug that a UTC test never can: 2024-03-
// 15T20:00Z is already the 16th in Jakarta.
func jakarta(t *testing.T) *time.Location {
	t.Helper()
	loc, err := LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("Asia/Jakarta: %v — the process should carry zoneinfo", err)
	}
	return loc
}

func TestPresetsResolveDayAlignedInTheDashboardsZone(t *testing.T) {
	loc := jakarta(t)
	// 20:00 UTC on the 14th is 03:00 on the 15th in Jakarta, which is the whole
	// point of resolving in the dashboard's own zone.
	now := time.Date(2024, 3, 14, 20, 0, 0, 0, time.UTC)

	cases := map[Preset]struct{ from, to string }{
		PresetLast7d:    {"2024-03-09", "2024-03-16"},
		PresetLast30d:   {"2024-02-15", "2024-03-16"},
		PresetMTD:       {"2024-03-01", "2024-03-16"},
		PresetQTD:       {"2024-01-01", "2024-03-16"},
		PresetYTD:       {"2024-01-01", "2024-03-16"},
		PresetLastMonth: {"2024-02-01", "2024-03-01"},
	}
	for p, want := range cases {
		w, err := p.Resolve(now, loc)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got := w.From.Format("2006-01-02"); got != want.from {
			t.Errorf("%s from = %s, want %s", p, got, want.from)
		}
		if got := w.To.Format("2006-01-02"); got != want.to {
			t.Errorf("%s to = %s, want %s", p, got, want.to)
		}
		if w.From.Location() != loc || w.To.Location() != loc {
			t.Errorf("%s resolved outside the dashboard's zone", p)
		}
	}
}

// The windows that include today must run to tomorrow's midnight, because To is
// exclusive: a month-to-date that stops at this morning drops today's orders and
// looks like a slow day.
func TestTodayIsInsideTheWindowsThatIncludeIt(t *testing.T) {
	loc := jakarta(t)
	now := time.Date(2024, 3, 14, 20, 0, 0, 0, time.UTC) // the 15th, 03:00, in Jakarta
	for _, p := range []Preset{PresetLast7d, PresetLast30d, PresetMTD, PresetQTD, PresetYTD} {
		w, err := p.Resolve(now, loc)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		endOfToday := time.Date(2024, 3, 15, 23, 59, 59, 0, loc)
		if !endOfToday.Before(w.To) {
			t.Errorf("%s ends at %s, which drops rows written later today", p, w.To)
		}
	}
}

// A quarter-to-date on the first day of a quarter is that day, not the previous
// quarter — the off-by-one this arithmetic invites.
func TestQuarterToDateOnTheFirstDayOfAQuarter(t *testing.T) {
	loc := jakarta(t)
	w, err := PresetQTD.Resolve(time.Date(2024, 7, 1, 6, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.From.Format("2006-01-02"); got != "2024-07-01" {
		t.Errorf("qtd from = %s, want 2024-07-01", got)
	}
}

func TestLastMonthCrossesTheYearBoundary(t *testing.T) {
	loc := jakarta(t)
	w, err := PresetLastMonth.Resolve(time.Date(2024, 1, 9, 6, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.From.Format("2006-01-02") + "→" + w.To.Format("2006-01-02"); got != "2023-12-01→2024-01-01" {
		t.Errorf("last_month = %s, want 2023-12-01→2024-01-01", got)
	}
}

func TestUnknownPresetNamesTheOnesThatWork(t *testing.T) {
	_, err := Preset("last_week").Resolve(time.Now(), time.UTC)
	if err == nil {
		t.Fatal("an unknown preset must be refused")
	}
	if got := err.Error(); !strings.Contains(got, "last_7d") {
		t.Errorf("the error should list the presets, got %q", got)
	}
}

// An unknown zone is an error, not a silent UTC: a dashboard whose windows
// quietly shifted seven hours is a support ticket nobody can reproduce.
func TestUnknownTimeZoneIsRefused(t *testing.T) {
	if _, err := LoadLocation("Mars/Olympus"); err == nil {
		t.Error("an unknown zone must be refused")
	}
	loc, err := LoadLocation("")
	if err != nil || loc != time.UTC {
		t.Errorf("an empty zone must be UTC, got %v %v", loc, err)
	}
}
