package app

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
)

// windowConn answers like a real `COALESCE(SUM(x), 0)` template does: a window
// that overlaps the data returns a number, and a window that does not returns
// **0 rather than NULL**. That conversion is the whole bug — it is why
// Evaluation.Empty never fires on the metric path and why "Rp 0" reached a
// question about a quarter the warehouse does not hold.
type windowConn struct {
	dataFrom, dataTo time.Time
	calls            int
	failFrom         *time.Time // when set, a probe starting here errors
}

func (c *windowConn) ExecuteReadOnly(context.Context, string, int) (*db.QueryResult, error) {
	return nil, nil
}

func (c *windowConn) ExecuteReadOnlyParams(_ context.Context, _ string, args []any, _ int) (*db.QueryResult, error) {
	c.calls++
	from, _ := args[0].(time.Time)
	to, _ := args[1].(time.Time)
	if c.failFrom != nil && from.Equal(*c.failFrom) {
		return nil, errors.New("warehouse timed out")
	}
	if from.Before(c.dataTo) && to.After(c.dataFrom) {
		return oneRow("v", int64(1000)), nil
	}
	return oneRow("v", int64(0)), nil
}

func (c *windowConn) ExtractSchema(context.Context) (*db.SchemaMetadata, error) { return nil, nil }
func (c *windowConn) Ping(context.Context) error                                { return nil }
func (c *windowConn) Close() error                                              { return nil }

// anyPool hands back whichever Conn the test is driving. The package's own
// fakePool is typed to *fakeConn, which cannot answer differently per window —
// and answering differently per window is the entire subject here.
type anyPool struct{ conn db.Conn }

func (p anyPool) For(context.Context, string, string) (db.Conn, error) { return p.conn, nil }

func serviceOver(conn db.Conn) *MetricService {
	m := metricFixture()
	return NewMetricService(
		&fakeMetricRepo{byKey: map[string]*domain.MetricDefinition{m.Key: m}},
		fakeSource{}, anyPool{conn: conn},
	)
}

func day(y int, mth time.Month, d int) time.Time {
	return time.Date(y, mth, d, 0, 0, 0, 0, time.UTC)
}

// The eval case, in a unit test: sales for Q3 2025 against data that ends on
// 31 December 2024. Before this the tool reported a genuine 0 with a hedge, and
// both models said "Rp 0".
func TestZeroAfterTheEndOfTheDataIsNotAnAnswer(t *testing.T) {
	conn := &windowConn{dataFrom: day(2023, time.January, 1), dataTo: day(2025, time.January, 1)}
	svc := serviceOver(conn)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2025, time.July, 1), day(2025, time.October, 1), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Zero == nil {
		t.Fatal("a zero was returned with no coverage verdict")
	}
	if got := res.Primary.Zero.Verdict; got != metric.ZeroAfterCoverage {
		t.Errorf("verdict = %q, want %q", got, metric.ZeroAfterCoverage)
	}
	if res.Primary.Zero.Before == nil || *res.Primary.Zero.Before == 0 {
		t.Errorf("before = %v, want the non-zero value that proves data exists earlier", res.Primary.Zero.Before)
	}
	// One evaluation plus two probes, and not one query more.
	if conn.calls != 3 {
		t.Errorf("ran %d queries, want 3 (the metric and its two side probes)", conn.calls)
	}
}

func TestZeroBeforeTheDataBeginsIsNotAnAnswer(t *testing.T) {
	conn := &windowConn{dataFrom: day(2023, time.January, 1), dataTo: day(2025, time.January, 1)}
	svc := serviceOver(conn)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2019, time.January, 1), day(2019, time.April, 1), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Zero == nil || res.Primary.Zero.Verdict != metric.ZeroBeforeCoverage {
		t.Fatalf("verdict = %+v, want %q", res.Primary.Zero, metric.ZeroBeforeCoverage)
	}
	if res.Primary.Zero.After == nil || *res.Primary.Zero.After == 0 {
		t.Errorf("after = %v, want the non-zero value that proves data exists later", res.Primary.Zero.After)
	}
}

// The other half, and the half that makes the first half worth believing: a
// window the data covers, which really did total nothing, must be reported as a
// plain 0 with no coverage caveat at all.
func TestZeroInsideTheDataIsAGenuineZero(t *testing.T) {
	// Data on both sides of a quiet February.
	conn := &quietMonthConn{
		quietFrom: day(2024, time.February, 1),
		quietTo:   day(2024, time.March, 1),
	}
	svc := serviceOver(conn)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2024, time.February, 1), day(2024, time.March, 1), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Zero == nil || res.Primary.Zero.Verdict != metric.ZeroInsideCoverage {
		t.Fatalf("verdict = %+v, want %q", res.Primary.Zero, metric.ZeroInsideCoverage)
	}
}

// quietMonthConn returns 0 for exactly one window and a number for every other,
// which is what a real month with no sales looks like.
type quietMonthConn struct {
	quietFrom, quietTo time.Time
	calls              int
}

func (c *quietMonthConn) ExecuteReadOnly(context.Context, string, int) (*db.QueryResult, error) {
	return nil, nil
}

func (c *quietMonthConn) ExecuteReadOnlyParams(_ context.Context, _ string, args []any, _ int) (*db.QueryResult, error) {
	c.calls++
	from, _ := args[0].(time.Time)
	to, _ := args[1].(time.Time)
	if from.Equal(c.quietFrom) && to.Equal(c.quietTo) {
		return oneRow("v", int64(0)), nil
	}
	return oneRow("v", int64(1000)), nil
}

func (c *quietMonthConn) ExtractSchema(context.Context) (*db.SchemaMetadata, error) {
	return nil, nil
}
func (c *quietMonthConn) Ping(context.Context) error { return nil }
func (c *quietMonthConn) Close() error               { return nil }

// A metric that is zero over everything is a definition or a loading problem,
// and saying so is more useful than either "0" or "no data for this period".
func TestZeroEverywhereIsReportedAsSuch(t *testing.T) {
	// Data range that overlaps nothing: every window returns 0.
	conn := &windowConn{dataFrom: day(2100, time.January, 1), dataTo: day(2100, time.January, 2)}
	svc := serviceOver(conn)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2024, time.January, 1), day(2024, time.February, 1), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Zero == nil || res.Primary.Zero.Verdict != metric.ZeroEverywhere {
		t.Fatalf("verdict = %+v, want %q", res.Primary.Zero, metric.ZeroEverywhere)
	}
}

// Half a verdict is worse than none: a probe that could not run must leave the
// hedged note in place rather than produce a confident sentence from one side.
func TestAFailedProbeLeavesNoVerdict(t *testing.T) {
	floor := metric.AllTimeWindow(time.Now()).From
	conn := &windowConn{
		dataFrom: day(2023, time.January, 1),
		dataTo:   day(2025, time.January, 1),
		failFrom: &floor, // the "everything before" probe errors
	}
	svc := serviceOver(conn)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2025, time.July, 1), day(2025, time.October, 1), "")
	if err != nil {
		t.Fatalf("Query must still answer when a probe fails: %v", err)
	}
	if res.Primary.Zero != nil {
		t.Errorf("verdict = %+v, want none — one side told us nothing", res.Primary.Zero)
	}
}

// The switch is a switch: off means one query, exactly as before this landed.
func TestZeroProbeCanBeTurnedOff(t *testing.T) {
	conn := &windowConn{dataFrom: day(2023, time.January, 1), dataTo: day(2025, time.January, 1)}
	svc := serviceOver(conn).WithZeroCoverageProbe(false)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2025, time.July, 1), day(2025, time.October, 1), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Zero != nil {
		t.Errorf("probe is off but a verdict came back: %+v", res.Primary.Zero)
	}
	if conn.calls != 1 {
		t.Errorf("ran %d queries with the probe off, want 1", conn.calls)
	}
}

// A non-zero answer never pays for the probe.
func TestAnOrdinaryAnswerRunsOneQuery(t *testing.T) {
	conn := &windowConn{dataFrom: day(2023, time.January, 1), dataTo: day(2025, time.January, 1)}
	svc := serviceOver(conn)

	res, err := svc.Query(context.Background(), "co-1", "revenue",
		day(2024, time.January, 1), day(2024, time.February, 1), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Value != 1000 {
		t.Errorf("value = %v, want 1000", res.Primary.Value)
	}
	if res.Primary.Zero != nil {
		t.Errorf("a non-zero answer carries a zero verdict: %+v", res.Primary.Zero)
	}
	if conn.calls != 1 {
		t.Errorf("ran %d queries for an ordinary answer, want 1", conn.calls)
	}
}
