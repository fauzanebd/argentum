package app

import (
	"strings"
	"testing"
	"time"
)

func TestValidateCron(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		tz      string
		wantErr string // "" means it must validate
	}{
		{name: "every day at nine", spec: "0 9 * * *", tz: "UTC"},
		{name: "with a timezone", spec: "0 9 * * *", tz: "Asia/Jakarta"},
		{name: "with an empty timezone", spec: "0 9 * * *", tz: ""},
		{name: "every fifteen minutes", spec: "*/15 * * * *", tz: "UTC"},
		{name: "weekday range", spec: "0 8 * * 1-5", tz: "Asia/Jakarta"},
		{name: "list", spec: "0 8,12,18 * * *", tz: "UTC"},
		{name: "day of month", spec: "0 6 1 * *", tz: "UTC"},

		{name: "empty", spec: "", tz: "UTC", wantErr: "cron_expression required"},
		{name: "whitespace only", spec: "   ", tz: "UTC", wantErr: "cron_expression required"},
		{name: "not a cron expression", spec: "every morning", tz: "UTC", wantErr: "invalid cron_expression"},
		{name: "too few fields", spec: "0 9 * *", tz: "UTC", wantErr: "invalid cron_expression"},
		{name: "six fields", spec: "0 0 9 * * *", tz: "UTC", wantErr: "invalid cron_expression"},
		{name: "minute out of range", spec: "99 9 * * *", tz: "UTC", wantErr: "invalid cron_expression"},
		{name: "hour out of range", spec: "0 25 * * *", tz: "UTC", wantErr: "invalid cron_expression"},
		// The 5-field parser is built without cron.Descriptor, so the @-forms
		// are not accepted. Pinned because "@daily" is the first thing anyone
		// tries and the rejection has to be deliberate rather than incidental.
		{name: "@daily descriptor", spec: "@daily", tz: "UTC", wantErr: "invalid cron_expression"},
		{name: "@every descriptor", spec: "@every 1h", tz: "UTC", wantErr: "invalid cron_expression"},
		// An invalid zone reaches the parser as CRON_TZ=… and fails there.
		// Create/Update normalise first, so this is the belt to that braces.
		{name: "invalid timezone", spec: "0 9 * * *", tz: "Mars/Olympus", wantErr: "invalid cron_expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCron(tc.spec, tc.tz)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCron(%q, %q) = %v, want nil", tc.spec, tc.tz, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateCron(%q, %q) = nil, want an error", tc.spec, tc.tz)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeTimezone(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty defaults to UTC", in: "", want: "UTC"},
		{name: "whitespace defaults to UTC", in: "   ", want: "UTC"},
		{name: "explicit UTC", in: "UTC", want: "UTC"},
		{name: "jakarta", in: "Asia/Jakarta", want: "Asia/Jakarta"},
		{name: "new york", in: "America/New_York", want: "America/New_York"},
		{name: "london", in: "Europe/London", want: "Europe/London"},
		{name: "trimmed", in: "  Asia/Jakarta  ", want: "Asia/Jakarta"},
		{name: "Local is accepted by LoadLocation", in: "Local", want: "Local"},

		{name: "invented zone", in: "Mars/Olympus", wantErr: true},
		// Deliberately not asserted: whether "asia/jakarta" is accepted
		// depends on whether the zone lookup lands on a case-insensitive
		// filesystem (macOS) or the embedded zip (Linux, CI). Pinning either
		// answer would make this test pass on one platform and fail on the
		// other, and nothing in the product depends on the outcome.
		{name: "an offset is not a zone name", in: "+07:00", wantErr: true},
		{name: "a common abbreviation is not a zone name", in: "WIB", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTimezone(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeTimezone(%q) = %q, want an error", tc.in, got)
				}
				if got != "" {
					t.Errorf("normalizeTimezone(%q) = %q on error, want \"\"", tc.in, got)
				}
				if !strings.Contains(err.Error(), tc.in) {
					t.Errorf("err = %q, want it to quote the rejected value", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeTimezone(%q) = %v, want nil", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("normalizeTimezone(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The scheduler runs in Docker images built on bare `alpine:latest`, which has
// no /usr/share/zoneinfo. internal/app blank-imports time/tzdata so the zone
// database travels inside the binary; without it every one of these lookups
// fails in production and succeeds on every developer machine, which is the
// worst possible split. This test is the guard on that import.
func TestTimezoneDatabaseIsAvailableWithoutTheHostFilesystem(t *testing.T) {
	// ZONEINFO pointing at nothing removes the system directory from the
	// search, leaving only what is compiled in.
	t.Setenv("ZONEINFO", t.TempDir()+"/does-not-exist.zip")

	for _, name := range []string{"Asia/Jakarta", "America/New_York", "Europe/London", "Australia/Sydney"} {
		if _, err := time.LoadLocation(name); err != nil {
			t.Errorf("LoadLocation(%q) = %v with no host zoneinfo; the tzdata import is missing", name, err)
		}
	}
}

func TestNextFire(t *testing.T) {
	jakarta, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	t.Run("fires at the local wall-clock time, not UTC", func(t *testing.T) {
		// The whole point of storing a timezone: a tenant who asks for 09:00
		// means 09:00 where they are.
		after := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) // 19:00 in Jakarta
		got, err := nextFire("0 9 * * *", "Asia/Jakarta", after)
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}
		local := got.In(jakarta)
		if local.Hour() != 9 || local.Minute() != 0 {
			t.Errorf("next fire is %s local, want 09:00", local.Format(time.RFC3339))
		}
		if !local.After(after) {
			t.Errorf("next fire %s is not after %s", local, after)
		}
		// 09:00 WIB is 02:00 UTC — asserted so a regression that quietly
		// ignored CRON_TZ would fail here rather than look plausible.
		if got.UTC().Hour() != 2 {
			t.Errorf("next fire is %s UTC, want 02:00", got.UTC().Format(time.RFC3339))
		}
	})

	t.Run("an empty timezone is UTC", func(t *testing.T) {
		after := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
		withEmpty, err := nextFire("0 9 * * *", "", after)
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}
		withUTC, err := nextFire("0 9 * * *", "UTC", after)
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}
		if !withEmpty.Equal(withUTC) {
			t.Errorf("empty tz = %s, UTC = %s, want the same", withEmpty, withUTC)
		}
		if withUTC.UTC().Hour() != 9 {
			t.Errorf("next fire is %s, want 09:00 UTC", withUTC.UTC().Format(time.RFC3339))
		}
	})

	t.Run("an invalid spec errors rather than returning the zero time silently", func(t *testing.T) {
		got, err := nextFire("not a cron", "UTC", time.Now())
		if err == nil {
			t.Fatalf("nextFire = %v, want an error", got)
		}
		if !got.IsZero() {
			t.Errorf("time = %v on error, want the zero time", got)
		}
	})
}

// Both DST transitions are pinned as observed behaviour of robfig/cron. Neither
// is a bug in this code, but both are things a customer will notice, so they
// are recorded here rather than discovered in a support thread.
func TestNextFireAcrossDSTBoundaries(t *testing.T) {
	const tz = "America/New_York"
	ny, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	t.Run("a wall-clock time that does not exist is skipped, not shifted", func(t *testing.T) {
		// 2026-03-08 in New York jumps 02:00 → 03:00, so 02:30 never happens
		// that day. A daily 02:30 task does not run at all on that date; the
		// next fire is 02:30 the following day.
		after := time.Date(2026, 3, 7, 12, 0, 0, 0, ny)
		got, err := nextFire("30 2 * * *", tz, after)
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}
		local := got.In(ny)
		if local.Day() != 9 || local.Hour() != 2 || local.Minute() != 30 {
			t.Errorf("next fire = %s, want 2026-03-09 02:30 — the 8th has no 02:30", local.Format(time.RFC3339))
		}
	})

	t.Run("an hour before the gap still fires on the transition day", func(t *testing.T) {
		after := time.Date(2026, 3, 7, 12, 0, 0, 0, ny)
		got, err := nextFire("30 1 * * *", tz, after)
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}
		local := got.In(ny)
		if local.Day() != 8 || local.Hour() != 1 || local.Minute() != 30 {
			t.Errorf("next fire = %s, want 2026-03-08 01:30", local.Format(time.RFC3339))
		}
	})

	t.Run("a wall-clock time that happens twice fires twice", func(t *testing.T) {
		// 2026-11-01 in New York repeats 01:00–02:00: 01:30 occurs once at
		// -04:00 and again at -05:00. A daily 01:30 task therefore has two
		// valid fire times that day, an hour apart.
		first, err := nextFire("30 1 * * *", tz, time.Date(2026, 10, 31, 12, 0, 0, 0, ny))
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}
		second, err := nextFire("30 1 * * *", tz, first.Add(time.Minute))
		if err != nil {
			t.Fatalf("nextFire: %v", err)
		}

		firstLocal, secondLocal := first.In(ny), second.In(ny)
		if firstLocal.Hour() != 1 || firstLocal.Minute() != 30 {
			t.Fatalf("first fire = %s, want 01:30", firstLocal.Format(time.RFC3339))
		}
		if secondLocal.Hour() != 1 || secondLocal.Minute() != 30 || secondLocal.Day() != firstLocal.Day() {
			t.Fatalf("second fire = %s, want another 01:30 on the same date", secondLocal.Format(time.RFC3339))
		}
		if gap := second.Sub(first); gap != time.Hour {
			t.Errorf("the two 01:30 fires are %v apart, want 1h", gap)
		}
	})
}

func TestTruncateErr(t *testing.T) {
	// scheduled_task_runs.error_message is a bounded column and the string is
	// whatever a provider or driver produced, so the cap is load-bearing.
	if got := truncateErr("short"); got != "short" {
		t.Errorf("truncateErr(short) = %q, want it unchanged", got)
	}

	long := strings.Repeat("x", 2000)
	got := truncateErr(long)
	if len([]rune(got)) != 1025 {
		t.Errorf("truncated length = %d runes, want 1024 + the ellipsis", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q…, want it to end in an ellipsis", got[:20])
	}

	exact := strings.Repeat("x", 1024)
	if truncateErr(exact) != exact {
		t.Error("a string of exactly the cap was truncated")
	}
}
