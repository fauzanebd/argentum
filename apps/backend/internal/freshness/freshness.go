// Package freshness answers one question about a tenant's data source: when was
// it last loaded, and is that recent enough to answer from without saying so.
//
// It exists because every other accuracy mechanism in this product compares the
// answer to the result set. CheckGrounding (internal/guardrails/grounding.go)
// checks that every figure in the reply appears in the rows a tool returned;
// CheckFabrication checks that the turn retrieved anything at all. Both are
// blind to a result set that is a day old, so a warehouse whose nightly load
// failed at 02:00 produces an answer that is grounded, cited, non-fabricated
// and wrong — with every guard this product owns vouching for it.
//
// The shape is the one dbt's `source freshness` settled on: a tenant-supplied
// expression that reports when the data was loaded, and two thresholds over it.
// Nothing here infers freshness from information_schema or from max(created_at)
// on a table this product picked, because a guessed "as of" is a confident
// wrong claim about currency, and it would be wrong exactly where a warehouse
// is most interesting — a dimension table that legitimately has not changed in
// a month is not stale.
package freshness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// Verdict is what a probe concluded. The zero value is Unknown, which is the
// right default for every source that has never been configured.
type Verdict string

const (
	// Unknown means nothing is claimed. No expression is configured, or the
	// probe did not produce a timestamp this package could read.
	//
	// **Unknown is never reported as Stale**, and that distinction is the whole
	// reason this type has four values rather than a bool. A broken probe
	// announcing staleness would put a false caveat on a correct answer, and a
	// caveat users learn to ignore is worse than no caveat at all.
	Unknown Verdict = "unknown"
	// Fresh means the data loaded inside WarnAfter. Nothing is said to anyone.
	Fresh Verdict = "fresh"
	// Warn means the data is older than WarnAfter and younger than StaleAfter.
	// The answer carries the date; it does not carry a warning.
	Warn Verdict = "warn"
	// Stale means the data is older than StaleAfter. The answer carries a
	// notice the server wrote.
	Stale Verdict = "stale"
)

// Severity orders the verdicts so a turn that read two sources can be judged on
// the worse of them. Unknown sorts below Fresh deliberately: a turn that read
// one unchecked source and one fresh one has nothing to apologise for.
func (v Verdict) Severity() int {
	switch v {
	case Fresh:
		return 1
	case Warn:
		return 2
	case Stale:
		return 3
	default:
		return 0
	}
}

// Worse returns whichever verdict a reader should be told about.
func Worse(a, b Verdict) Verdict {
	if b.Severity() > a.Severity() {
		return b
	}
	return a
}

// Config is one source's freshness setup, stored on db_connections. The zero
// value means unchecked, which is what every row written before migration 079
// has to keep meaning — so a deployment that upgrades and configures nothing
// behaves exactly as it did.
type Config struct {
	// SQL is a single SELECT returning exactly one row and one column holding
	// the moment this source was last loaded.
	SQL string
	// WarnAfter and StaleAfter are how old the data may be before an answer
	// dates itself, and before it carries a notice. Zero means "no threshold at
	// this level": a source with only StaleAfter set never warns, it just goes
	// stale.
	WarnAfter  time.Duration
	StaleAfter time.Duration
}

// Configured reports whether this source has anything to probe.
func (c Config) Configured() bool { return strings.TrimSpace(c.SQL) != "" }

// Validate checks a config at save time rather than at 03:00 when a watcher
// fires. The SQL goes through sqlguard like every other piece of
// tenant-supplied SQL this product runs: a freshness expression is a statement
// we execute on a schedule against the customer's database, which is precisely
// the category T-H4 exists for.
func (c Config) Validate() error {
	if !c.Configured() {
		// Not an error. Clearing the expression is how a tenant turns this off.
		if c.WarnAfter != 0 || c.StaleAfter != 0 {
			return errors.New("a freshness threshold was set without an expression to measure against")
		}
		return nil
	}
	if err := sqlguard.ValidateStatement(c.SQL, nil); err != nil {
		return fmt.Errorf("freshness expression: %w", err)
	}
	if c.WarnAfter < 0 || c.StaleAfter < 0 {
		return errors.New("freshness thresholds cannot be negative")
	}
	if c.StaleAfter == 0 {
		return errors.New("stale_after is required: without it nothing can ever be reported stale")
	}
	if c.WarnAfter > c.StaleAfter {
		return fmt.Errorf("warn_after (%s) must not exceed stale_after (%s), or the warning can never fire", c.WarnAfter, c.StaleAfter)
	}
	return nil
}

// Report is one probe's conclusion.
type Report struct {
	Verdict Verdict `json:"verdict"`
	// ObservedAt is the timestamp the source reported. Zero when Unknown.
	ObservedAt time.Time `json:"observed_at,omitempty"`
	// Age is how long ago that was, at the moment of the probe.
	Age time.Duration `json:"-"`
	// Note says, in one clause, what a reader needs to know. Empty for Fresh
	// and for Unknown — the two cases where the product has nothing to add.
	Note string `json:"note,omitempty"`
}

// Classify turns an observed load time into a verdict. Pure, so the whole
// threshold policy is a table test with no database in it.
func Classify(observedAt, now time.Time, c Config) Report {
	if observedAt.IsZero() {
		return Report{Verdict: Unknown}
	}
	age := now.Sub(observedAt)
	// A load time in the future is a clock disagreement between this process and
	// the tenant's database, not freshness. Treated as age zero rather than as a
	// negative age, so a warehouse seven hours ahead reads as fresh rather than
	// as impossibly fresh.
	if age < 0 {
		age = 0
	}
	r := Report{ObservedAt: observedAt, Age: age}
	switch {
	case c.StaleAfter > 0 && age >= c.StaleAfter:
		r.Verdict = Stale
		r.Note = fmt.Sprintf("this source last loaded %s ago (%s), which is beyond its %s staleness threshold",
			humanAge(age), observedAt.UTC().Format("2006-01-02 15:04 UTC"), humanAge(c.StaleAfter))
	case c.WarnAfter > 0 && age >= c.WarnAfter:
		r.Verdict = Warn
		r.Note = fmt.Sprintf("this source last loaded %s ago (%s)",
			humanAge(age), observedAt.UTC().Format("2006-01-02 15:04 UTC"))
	default:
		r.Verdict = Fresh
	}
	return r
}

// Reader is the one thing a probe needs from a tenant connection. Narrowed to a
// single method so this package can be tested without a database, and so it
// does not acquire the rest of db.Conn's surface. *db.Conn implementations
// satisfy it as they are.
type Reader interface {
	ExecuteReadOnlyParams(ctx context.Context, sql string, args []any, maxRows int) (*db.QueryResult, error)
}

// Probe runs the configured expression and classifies what came back.
//
// Every failure path returns Unknown with an error, and the caller's job is to
// log it once and say nothing to the user (decision 6 of the roadmap). The
// error is returned rather than swallowed so an operator can see *why* a source
// stopped reporting, and so the Test endpoint can show an admin the actual
// message instead of an unexplained "unknown".
func Probe(ctx context.Context, r Reader, c Config, now time.Time) (Report, error) {
	if !c.Configured() {
		return Report{Verdict: Unknown}, nil
	}
	if r == nil {
		return Report{Verdict: Unknown}, errors.New("no connection to probe")
	}
	// maxRows 2, not 1: asking for two is how the "exactly one row" rule below
	// can tell a single-row answer from a truncated one. Capped at 1 it would be
	// impossible to distinguish "one row" from "the first of many", and an
	// expression returning a row per table would silently report whichever row
	// the database happened to order first.
	res, err := r.ExecuteReadOnlyParams(ctx, c.SQL, nil, 2)
	if err != nil {
		return Report{Verdict: Unknown}, fmt.Errorf("freshness probe failed: %w", err)
	}
	ts, err := singleTimestamp(res)
	if err != nil {
		return Report{Verdict: Unknown}, err
	}
	return Classify(ts, now, c), nil
}

// singleTimestamp enforces the expression's contract: one row, one column, a
// value that is a moment in time.
func singleTimestamp(res *db.QueryResult) (time.Time, error) {
	if res == nil || len(res.Rows) == 0 {
		return time.Time{}, errors.New("the freshness expression returned no rows")
	}
	if len(res.Rows) > 1 {
		return time.Time{}, fmt.Errorf("the freshness expression returned %d rows; it must return exactly one", len(res.Rows))
	}
	row := res.Rows[0]
	if len(row) != 1 {
		return time.Time{}, fmt.Errorf("the freshness expression returned %d columns; it must return exactly one", len(row))
	}
	var v any
	for _, val := range row {
		v = val
	}
	ts, ok := parseTimestamp(v)
	if !ok {
		return time.Time{}, fmt.Errorf("the freshness expression returned %T, which is not a timestamp", v)
	}
	return ts, nil
}

// timestampLayouts are tried in order against a string value. The drivers hand
// back a time.Time for a genuine timestamp column, so this list is for the
// cases where they do not: MySQL's text protocol, a column that is a formatted
// string, and an epoch stored as text.
//
// **A layout without a zone is read as UTC**, which is a guess and is the one
// guess this package makes. It is exposed rather than hidden: the Test endpoint
// returns the parsed instant, so an admin in Jakarta configuring a naive
// timestamp sees an age that is seven hours out and fixes the expression with a
// cast rather than discovering it from a wrong caveat weeks later.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999-07:00",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseTimestamp(v any) (time.Time, bool) {
	switch t := v.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		if t.IsZero() {
			return time.Time{}, false
		}
		return t, true
	case *time.Time:
		if t == nil || t.IsZero() {
			return time.Time{}, false
		}
		return *t, true
	case []byte:
		return parseTimestampString(string(t))
	case string:
		return parseTimestampString(t)
	}
	return time.Time{}, false
}

func parseTimestampString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range timestampLayouts {
		if ts, err := time.Parse(layout, s); err == nil && !ts.IsZero() {
			return ts, true
		}
	}
	return time.Time{}, false
}

// humanAge renders a duration the way a sentence in an answer wants it, not the
// way Go does: "2 days" rather than "51h13m9.2s". Deliberately coarse — the
// difference between 51 and 52 hours does not change what a reader does.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
