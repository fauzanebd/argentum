package domain

import (
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// The table the ticket asks for, and the thing it is really asserting: the
// same phrase, the same instant, two fiscal years, two different answers.
func TestFiscalQuarters(t *testing.T) {
	for _, tc := range []struct {
		name     string
		now      time.Time
		fyStart  int
		phrase   string
		from, to time.Time
	}{
		// An April fiscal year has Apr-Jun, Jul-Sep, Oct-Dec, Jan-Mar. Asked
		// in April, "last quarter" is Jan-Mar — which on a calendar year is
		// the quarter that *just ended* too, so April is the month where the
		// two conventions agree and the next case is what separates them.
		{"april fy, asked in april", date(2026, time.April, 15), 4, "last quarter", date(2026, time.January, 1), date(2026, time.April, 1)},
		// Asked in May: an April fiscal year is one month into Apr-Jun, so
		// last quarter is still Jan-Mar. A calendar year is two months into
		// Apr-Jun, so last quarter is Jan-Mar as well — they agree again.
		{"january fy, asked in may", date(2026, time.May, 15), 1, "last quarter", date(2026, time.January, 1), date(2026, time.April, 1)},
		// And here they do not. A January fiscal year asked in January is in
		// Q1, so last quarter is the previous *year's* Oct-Dec.
		{"january fy, asked in january", date(2026, time.January, 15), 1, "last quarter", date(2025, time.October, 1), date(2026, time.January, 1)},
		// The same instant against an April fiscal year: January is inside
		// Jan-Mar, the fourth quarter, so last quarter is Oct-Dec — the same
		// three months, reached a different way.
		{"april fy, asked in january", date(2026, time.January, 15), 4, "last quarter", date(2025, time.October, 1), date(2026, time.January, 1)},
		// The case that separates them cleanly: July.
		{"april fy, asked in july", date(2026, time.July, 10), 4, "last quarter", date(2026, time.April, 1), date(2026, time.July, 1)},
		{"january fy, asked in july", date(2026, time.July, 10), 1, "last quarter", date(2026, time.April, 1), date(2026, time.July, 1)},
		// And August, where the fiscal offset finally shows: an April year is
		// in Jul-Sep, a calendar year is in Jul-Sep too. September likewise.
		// October is where they part: April year → Oct-Dec is a NEW quarter,
		// calendar year → Oct-Dec is also new. The quarters of an April
		// fiscal year and a January one differ only in which is numbered Q1,
		// because 4 is itself a quarter boundary. A May fiscal year is what
		// actually shifts the blocks.
		{"may fy, asked in july", date(2026, time.July, 10), 5, "last quarter", date(2026, time.February, 1), date(2026, time.May, 1)},
		{"this quarter, may fy", date(2026, time.July, 10), 5, "this quarter", date(2026, time.May, 1), date(2026, time.August, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FiscalPeriod(tc.phrase, tc.now, tc.fyStart)
			if !ok {
				t.Fatalf("FiscalPeriod(%q) was not recognised", tc.phrase)
			}
			if !got.From.Equal(tc.from) || !got.To.Equal(tc.to) {
				t.Errorf("FiscalPeriod(%q) = %s..%s, want %s..%s",
					tc.phrase,
					got.From.Format("2006-01-02"), got.To.Format("2006-01-02"),
					tc.from.Format("2006-01-02"), tc.to.Format("2006-01-02"))
			}
		})
	}
}

// A May fiscal year is the one that actually moves the blocks, because 1, 4, 7
// and 10 are quarter boundaries on a calendar year and 5 is not. Without this
// the table above could pass on an implementation that ignored fyStart
// entirely for two of its rows.
func TestAFiscalYearThatIsNotOnAQuarterBoundary(t *testing.T) {
	now := date(2026, time.June, 20)
	calendar, _ := FiscalPeriod("this quarter", now, 1)
	fiscal, _ := FiscalPeriod("this quarter", now, 5)
	if calendar.From.Equal(fiscal.From) {
		t.Fatalf("a May fiscal year and a calendar year agree on this quarter (%s); fyStart is being ignored",
			calendar.From.Format("2006-01-02"))
	}
	if want := date(2026, time.April, 1); !calendar.From.Equal(want) {
		t.Errorf("calendar this quarter = %s, want %s", calendar.From.Format("2006-01-02"), want.Format("2006-01-02"))
	}
	if want := date(2026, time.May, 1); !fiscal.From.Equal(want) {
		t.Errorf("May-fiscal this quarter = %s, want %s", fiscal.From.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestMonthsAndYears(t *testing.T) {
	now := date(2026, time.March, 9)
	for _, tc := range []struct {
		phrase   string
		fyStart  int
		from, to time.Time
	}{
		{"this month", 1, date(2026, time.March, 1), date(2026, time.April, 1)},
		{"last month", 1, date(2026, time.February, 1), date(2026, time.March, 1)},
		// A fiscal month does not change what a calendar month is.
		{"last month", 4, date(2026, time.February, 1), date(2026, time.March, 1)},
		// YTD runs to tomorrow, because the range is closed-open and today's
		// rows are the whole point of asking.
		{"ytd", 1, date(2026, time.January, 1), date(2026, time.March, 10)},
		// An April fiscal year in March is still in the year that began last
		// April.
		{"ytd", 4, date(2025, time.April, 1), date(2026, time.March, 10)},
		{"last year", 1, date(2025, time.January, 1), date(2026, time.January, 1)},
		{"last year", 4, date(2024, time.April, 1), date(2025, time.April, 1)},
	} {
		got, ok := FiscalPeriod(tc.phrase, now, tc.fyStart)
		if !ok {
			t.Fatalf("FiscalPeriod(%q) was not recognised", tc.phrase)
		}
		if !got.From.Equal(tc.from) || !got.To.Equal(tc.to) {
			t.Errorf("FiscalPeriod(%q, fy=%d) = %s..%s, want %s..%s", tc.phrase, tc.fyStart,
				got.From.Format("2006-01-02"), got.To.Format("2006-01-02"),
				tc.from.Format("2006-01-02"), tc.to.Format("2006-01-02"))
		}
	}
}

// An unrecognised phrase is refused rather than guessed at: a period quietly
// resolved to the wrong three months is the error this file exists to prevent.
func TestAnUnknownPhraseIsRefused(t *testing.T) {
	for _, phrase := range []string{"", "last fortnight", "since the merger", "H1", "next quarter"} {
		if _, ok := FiscalPeriod(phrase, date(2026, time.March, 9), 1); ok {
			t.Errorf("FiscalPeriod(%q) was resolved; it should be refused", phrase)
		}
	}
}

func TestSpellingsAModelActuallyWrites(t *testing.T) {
	now := date(2026, time.March, 9)
	want, _ := FiscalPeriod("last quarter", now, 1)
	for _, phrase := range []string{"Last Quarter", "last_quarter", "last-quarter", "previous quarter", "  LAST   QUARTER  "} {
		got, ok := FiscalPeriod(phrase, now, 1)
		if !ok || !got.From.Equal(want.From) || !got.To.Equal(want.To) {
			t.Errorf("FiscalPeriod(%q) did not resolve to last quarter: %+v ok=%v", phrase, got, ok)
		}
	}
}

// Days is what a run-rate divides by, and a quarter is not 90 days just
// because three months sounds like it.
func TestDaysAndSQL(t *testing.T) {
	p, _ := FiscalPeriod("last quarter", date(2026, time.May, 1), 1)
	if got := p.Days(); got != 90 {
		t.Errorf("Jan-Mar 2026 is %d days, want 90", got)
	}
	leap, _ := FiscalPeriod("last quarter", date(2024, time.May, 1), 1)
	if got := leap.Days(); got != 91 {
		t.Errorf("Jan-Mar 2024 is a leap quarter: %d days, want 91", got)
	}
	if got, want := p.SQL(), ">= '2026-01-01' AND < '2026-04-01'"; got != want {
		t.Errorf("SQL() = %q, want %q", got, want)
	}
}
