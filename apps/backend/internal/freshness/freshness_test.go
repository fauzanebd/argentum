package freshness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

var now = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

func cfg(warn, stale time.Duration) Config {
	return Config{SQL: "SELECT max(loaded_at) FROM etl_runs", WarnAfter: warn, StaleAfter: stale}
}

func TestClassifyWalksTheThresholds(t *testing.T) {
	c := cfg(2*time.Hour, 24*time.Hour)
	cases := []struct {
		name string
		age  time.Duration
		want Verdict
	}{
		{"just loaded", 0, Fresh},
		{"inside warn", 90 * time.Minute, Fresh},
		{"exactly warn", 2 * time.Hour, Warn},
		{"between", 6 * time.Hour, Warn},
		{"exactly stale", 24 * time.Hour, Stale},
		{"well past", 72 * time.Hour, Stale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(now.Add(-tc.age), now, c)
			if got.Verdict != tc.want {
				t.Errorf("age %s: verdict = %q; want %q", tc.age, got.Verdict, tc.want)
			}
			if got.Age != tc.age {
				t.Errorf("age = %s; want %s", got.Age, tc.age)
			}
		})
	}
}

// A zero observed time is the only way Classify can be told "nothing came back",
// and it must never resolve to Stale — the distinction decision 6 turns on.
func TestAnUnobservedLoadTimeIsUnknownNotStale(t *testing.T) {
	got := Classify(time.Time{}, now, cfg(time.Hour, time.Hour))
	if got.Verdict != Unknown {
		t.Fatalf("verdict = %q; want %q", got.Verdict, Unknown)
	}
	if got.Note != "" {
		t.Errorf("note = %q; unknown says nothing", got.Note)
	}
}

// A source with only a stale threshold never warns. Somebody who wants one
// signal rather than two should get one.
func TestAConfigWithNoWarnThresholdNeverWarns(t *testing.T) {
	c := cfg(0, 24*time.Hour)
	if got := Classify(now.Add(-20*time.Hour), now, c); got.Verdict != Fresh {
		t.Errorf("verdict = %q; want %q with no warn threshold set", got.Verdict, Fresh)
	}
	if got := Classify(now.Add(-25*time.Hour), now, c); got.Verdict != Stale {
		t.Errorf("verdict = %q; want %q", got.Verdict, Stale)
	}
}

// A warehouse whose clock is ahead of ours must read as fresh, not as
// impossibly fresh or as a negative age in a sentence somebody reads.
func TestAFutureLoadTimeIsFreshRatherThanNegative(t *testing.T) {
	got := Classify(now.Add(7*time.Hour), now, cfg(time.Hour, 2*time.Hour))
	if got.Verdict != Fresh {
		t.Errorf("verdict = %q; want %q", got.Verdict, Fresh)
	}
	if got.Age < 0 {
		t.Errorf("age = %s; a negative age reaches a sentence somebody reads", got.Age)
	}
}

func TestOnlyTheTwoActionableVerdictsCarryANote(t *testing.T) {
	c := cfg(2*time.Hour, 24*time.Hour)
	if n := Classify(now, now, c).Note; n != "" {
		t.Errorf("fresh note = %q; want empty", n)
	}
	warn := Classify(now.Add(-3*time.Hour), now, c)
	if !strings.Contains(warn.Note, "3 hours ago") {
		t.Errorf("warn note = %q; want the age in it", warn.Note)
	}
	if strings.Contains(warn.Note, "threshold") {
		t.Errorf("warn note = %q; a warning is a date, not a complaint", warn.Note)
	}
	stale := Classify(now.Add(-50*time.Hour), now, c)
	if !strings.Contains(stale.Note, "2 days ago") || !strings.Contains(stale.Note, "threshold") {
		t.Errorf("stale note = %q; want the age and the threshold", stale.Note)
	}
}

func TestWorseKeepsTheVerdictAReaderNeeds(t *testing.T) {
	cases := []struct{ a, b, want Verdict }{
		{Fresh, Stale, Stale},
		{Stale, Fresh, Stale},
		{Warn, Stale, Stale},
		{Fresh, Warn, Warn},
		{Unknown, Fresh, Fresh},
		{Fresh, Unknown, Fresh},
		{Unknown, Unknown, Unknown},
	}
	for _, tc := range cases {
		if got := Worse(tc.a, tc.b); got != tc.want {
			t.Errorf("Worse(%q, %q) = %q; want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---- Validate ----

func TestValidateRefusesAStatementThatIsNotARead(t *testing.T) {
	for _, sql := range []string{
		"DELETE FROM etl_runs",
		"SELECT max(loaded_at) FROM etl_runs; DROP TABLE etl_runs",
		"UPDATE etl_runs SET loaded_at = now()",
	} {
		c := Config{SQL: sql, StaleAfter: time.Hour}
		if err := c.Validate(); err == nil {
			t.Errorf("Validate(%q) = nil; a freshness expression runs on a schedule against a customer's database", sql)
		}
	}
}

func TestValidateAcceptsAnOrdinaryRead(t *testing.T) {
	c := Config{SQL: "WITH last AS (SELECT max(loaded_at) AS t FROM etl_runs) SELECT t FROM last", WarnAfter: time.Hour, StaleAfter: 24 * time.Hour}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v; want nil", err)
	}
}

func TestClearingTheExpressionIsHowYouTurnItOff(t *testing.T) {
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("the zero config is unchecked, not invalid: %v", err)
	}
	// But a threshold with nothing to measure is a setting that silently does
	// nothing, which is worse than a refusal.
	if err := (Config{StaleAfter: time.Hour}).Validate(); err == nil {
		t.Error("a threshold with no expression was accepted")
	}
}

func TestValidateRefusesThresholdsThatCannotBothFire(t *testing.T) {
	c := Config{SQL: "SELECT max(loaded_at) FROM etl_runs", WarnAfter: 48 * time.Hour, StaleAfter: 24 * time.Hour}
	if err := c.Validate(); err == nil {
		t.Error("warn_after past stale_after was accepted; the warning could never fire")
	}
	if err := (Config{SQL: "SELECT 1"}).Validate(); err == nil {
		t.Error("an expression with no stale threshold was accepted; nothing could ever be reported stale")
	}
}

// ---- Probe ----

type fakeReader struct {
	res     *db.QueryResult
	err     error
	calls   int
	lastSQL string
	maxRows int
}

func (f *fakeReader) ExecuteReadOnlyParams(_ context.Context, sql string, _ []any, maxRows int) (*db.QueryResult, error) {
	f.calls++
	f.lastSQL = sql
	f.maxRows = maxRows
	return f.res, f.err
}

func oneRow(v any) *db.QueryResult {
	return &db.QueryResult{Columns: []string{"loaded_at"}, Rows: []map[string]interface{}{{"loaded_at": v}}, Count: 1}
}

func TestProbeReadsATimestampAndClassifiesIt(t *testing.T) {
	r := &fakeReader{res: oneRow(now.Add(-30 * time.Minute))}
	got, err := Probe(context.Background(), r, cfg(2*time.Hour, 24*time.Hour), now)
	if err != nil {
		t.Fatalf("Probe() = %v", err)
	}
	if got.Verdict != Fresh {
		t.Errorf("verdict = %q; want %q", got.Verdict, Fresh)
	}
	if r.calls != 1 {
		t.Errorf("calls = %d; want 1", r.calls)
	}
	// Two, so "one row" can be told from "the first of many".
	if r.maxRows != 2 {
		t.Errorf("maxRows = %d; want 2", r.maxRows)
	}
}

func TestAnUnconfiguredSourceIsNeverProbed(t *testing.T) {
	r := &fakeReader{res: oneRow(now)}
	got, err := Probe(context.Background(), r, Config{}, now)
	if err != nil {
		t.Fatalf("Probe() = %v", err)
	}
	if got.Verdict != Unknown {
		t.Errorf("verdict = %q; want %q", got.Verdict, Unknown)
	}
	if r.calls != 0 {
		t.Errorf("calls = %d; an unconfigured source must not touch the tenant's database", r.calls)
	}
}

// The acceptance item this ticket turns on: every way a probe can fail is
// Unknown, and not one of them is Stale.
func TestEveryProbeFailureIsUnknownAndNeverStale(t *testing.T) {
	c := cfg(time.Minute, time.Minute) // thresholds so tight that anything real would be stale
	cases := []struct {
		name string
		r    *fakeReader
	}{
		{"the query errored", &fakeReader{err: errors.New("connection reset")}},
		{"no rows", &fakeReader{res: &db.QueryResult{Columns: []string{"loaded_at"}}}},
		{"nil result", &fakeReader{}},
		{"two rows", &fakeReader{res: &db.QueryResult{
			Columns: []string{"loaded_at"},
			Rows:    []map[string]interface{}{{"loaded_at": now}, {"loaded_at": now}},
		}}},
		{"two columns", &fakeReader{res: &db.QueryResult{
			Columns: []string{"a", "b"},
			Rows:    []map[string]interface{}{{"a": now, "b": now}},
		}}},
		{"not a timestamp", &fakeReader{res: oneRow(42)}},
		{"a null", &fakeReader{res: oneRow(nil)}},
		{"unparseable text", &fakeReader{res: oneRow("last tuesday")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Probe(context.Background(), tc.r, c, now)
			if got.Verdict != Unknown {
				t.Errorf("verdict = %q; want %q — a broken probe must never announce staleness", got.Verdict, Unknown)
			}
			if err == nil {
				t.Error("err = nil; an operator has to be able to see why a source stopped reporting")
			}
			if got.Note != "" {
				t.Errorf("note = %q; unknown says nothing to the user", got.Note)
			}
		})
	}
}

// The drivers do not agree on what a timestamp column comes back as: lib/pq
// hands over a time.Time, MySQL's text protocol a []byte, and a column that is
// genuinely a string stays one.
func TestProbeReadsTheShapesTheDriversActuallyReturn(t *testing.T) {
	want := time.Date(2026, 9, 11, 3, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		v    any
	}{
		{"time.Time", want},
		{"*time.Time", &want},
		{"RFC3339 string", "2026-09-11T03:30:00Z"},
		{"postgres text", "2026-09-11 03:30:00"},
		{"mysql bytes", []byte("2026-09-11 03:30:00")},
		{"a date", "2026-09-11"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Probe(context.Background(), &fakeReader{res: oneRow(tc.v)}, cfg(time.Hour, 48*time.Hour), now)
			if err != nil {
				t.Fatalf("Probe() = %v", err)
			}
			if got.Verdict == Unknown {
				t.Fatalf("verdict = %q; the driver's own return shape was not read", got.Verdict)
			}
			if got.ObservedAt.IsZero() {
				t.Error("observed_at is zero on a successful probe")
			}
		})
	}
}

func TestHumanAgeReadsLikeASentence(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "less than a minute"},
		{time.Minute, "1 minute"},
		{45 * time.Minute, "45 minutes"},
		{time.Hour, "1 hour"},
		{26 * time.Hour, "26 hours"},
		{50 * time.Hour, "2 days"},
		{24 * 8 * time.Hour, "8 days"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%s) = %q; want %q", tc.d, got, tc.want)
		}
	}
}
