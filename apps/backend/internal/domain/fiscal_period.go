package domain

import (
	"fmt"
	"strings"
	"time"
)

// Period is a resolved date range, closed-open: From is included, To is not.
//
// **Closed-open because every other convention loses a day somewhere.** A
// range written `BETWEEN '2026-01-01' AND '2026-03-31'` silently drops
// everything timestamped on the 31st after midnight, which is the commonest
// off-by-one in analytical SQL and the hardest to notice: the answer is only
// slightly wrong, and only for the last day.
type Period struct {
	// Name is the phrase this was resolved from, normalised.
	Name string `json:"name"`
	// From is the first instant in the period.
	From time.Time `json:"from"`
	// To is the first instant after it.
	To time.Time `json:"to"`
}

// Days is how many whole days the period spans — the figure a run-rate or a
// per-day average divides by.
func (p Period) Days() int { return int(p.To.Sub(p.From).Hours() / 24) }

// SQL renders the range the way it must be written to be correct.
func (p Period) SQL() string {
	return fmt.Sprintf(">= '%s' AND < '%s'", p.From.Format("2006-01-02"), p.To.Format("2006-01-02"))
}

// String is the line the prompt carries.
func (p Period) String() string {
	return fmt.Sprintf("%s = %s up to but NOT including %s (%d days)",
		p.Name, p.From.Format("2006-01-02"), p.To.Format("2006-01-02"), p.Days())
}

// FiscalPeriodNames are the phrases this resolver knows, in the order a prompt
// block lists them.
var FiscalPeriodNames = []string{
	"this month", "last month", "this quarter", "last quarter", "ytd", "last year",
}

// FiscalPeriod resolves a phrase like "last quarter" against the company's
// fiscal year, in the tenant's own calendar.
//
// **Why this is not a formatting nicety.** The comment on
// CompanyProfile.FiscalYearStartMonth has said it since T-B1: the fiscal month
// *"changes what 'last quarter' means, which is the single most common way an
// analytics answer is right about the numbers and wrong about the period"* —
// and until now nothing that computes has read it. It was rendered into the
// prompt as a fact about the company and then left for the model to do
// calendar arithmetic on, which is the same class of error T-W1 closed for
// division.
//
// fiscalYearStartMonth outside 1-12 is read as January, which is what every
// caller with no profile has always done.
//
// now is passed in rather than read from the clock so this is testable and so
// one turn resolves every period against one instant. Its location is the
// tenant's calendar: the caller supplies a time already in the zone the
// answer will be read in.
func FiscalPeriod(name string, now time.Time, fiscalYearStartMonth int) (Period, bool) {
	key := normalisePeriod(name)
	fyStart := fiscalYearStartMonth
	if fyStart < 1 || fyStart > 12 {
		fyStart = 1
	}
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	switch key {
	case "this month":
		return Period{Name: key, From: monthStart, To: monthStart.AddDate(0, 1, 0)}, true
	case "last month":
		return Period{Name: key, From: monthStart.AddDate(0, -1, 0), To: monthStart}, true
	case "this quarter":
		q := fiscalQuarterStart(monthStart, fyStart, loc)
		return Period{Name: key, From: q, To: q.AddDate(0, 3, 0)}, true
	case "last quarter":
		q := fiscalQuarterStart(monthStart, fyStart, loc)
		return Period{Name: key, From: q.AddDate(0, -3, 0), To: q}, true
	case "ytd":
		y := fiscalYearStart(monthStart, fyStart, loc)
		// Up to and including today, which is what somebody asking for
		// year-to-date means: the "to" is tomorrow, because the range is
		// closed-open and today's rows are the point.
		return Period{Name: key, From: y, To: day.AddDate(0, 0, 1)}, true
	case "last year":
		y := fiscalYearStart(monthStart, fyStart, loc)
		return Period{Name: key, From: y.AddDate(-1, 0, 0), To: y}, true
	default:
		return Period{}, false
	}
}

// normalisePeriod folds the spellings a model will actually write into the six
// this resolver knows. It is deliberately small: an unrecognised phrase is
// refused rather than guessed at, because a period quietly resolved to the
// wrong three months is the error this whole file exists to prevent.
func normalisePeriod(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	switch s {
	case "mtd", "month to date", "current month":
		return "this month"
	case "previous month", "prior month":
		return "last month"
	case "qtd", "quarter to date", "current quarter":
		return "this quarter"
	case "previous quarter", "prior quarter":
		return "last quarter"
	case "year to date", "this year", "current year", "fiscal ytd":
		return "ytd"
	case "previous year", "prior year", "last fiscal year":
		return "last year"
	}
	return s
}

// fiscalYearStart is the first day of the fiscal year containing month.
func fiscalYearStart(month time.Time, fyStart int, loc *time.Location) time.Time {
	y := month.Year()
	if int(month.Month()) < fyStart {
		y--
	}
	return time.Date(y, time.Month(fyStart), 1, 0, 0, 0, 0, loc)
}

// fiscalQuarterStart is the first day of the fiscal quarter containing month.
//
// Quarters are three-month blocks counted from the fiscal year's start, so an
// April fiscal year has Apr-Jun, Jul-Sep, Oct-Dec, Jan-Mar — and "last
// quarter" asked in April is the Jan-Mar block, which on a calendar year would
// have been the current one.
func fiscalQuarterStart(month time.Time, fyStart int, loc *time.Location) time.Time {
	ys := fiscalYearStart(month, fyStart, loc)
	elapsed := int(month.Month()) - int(ys.Month())
	if elapsed < 0 {
		elapsed += 12
	}
	return ys.AddDate(0, (elapsed/3)*3, 0)
}
