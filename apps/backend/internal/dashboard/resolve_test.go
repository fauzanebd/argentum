package dashboard

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
)

// A driver registered for the tests only, so the resolver can ask for a dialect
// without a database behind it. Its placeholder syntax is Postgres', which is
// what the assertions read.
const fakeDBType = "dashboard_test_db"

type fakeDialect struct{}

func (fakeDialect) Type() string                                { return fakeDBType }
func (fakeDialect) StatementTimeoutPragma(time.Duration) string { return "" }
func (fakeDialect) ReadOnlyPragma() string                      { return "" }
func (fakeDialect) QuoteIdentifier(name string) string          { return `"` + name + `"` }
func (fakeDialect) Placeholder(n int) string                    { return "$" + strconv.Itoa(n) }

type fakeDriver struct{}

func (fakeDriver) Type() string        { return fakeDBType }
func (fakeDriver) Dialect() db.Dialect { return fakeDialect{} }
func (fakeDriver) Open(context.Context, string) (db.Conn, error) {
	return nil, errors.New("the test driver does not open connections")
}

func init() { db.Register(fakeDriver{}) }

// fakeConn answers one canned result per SQL fragment, and records what it was
// asked, so a test can assert on the rendered statement and the row cap.
type fakeConn struct {
	db.Conn
	answer  func(sql string, args []any, maxRows int) (*db.QueryResult, error)
	calls   atomic.Int32
	maxSeen atomic.Int32
}

func (c *fakeConn) ExecuteReadOnlyParams(_ context.Context, sql string, args []any, maxRows int) (*db.QueryResult, error) {
	c.calls.Add(1)
	c.maxSeen.Store(int32(maxRows))
	return c.answer(sql, args, maxRows)
}

type fakePool struct{ conn *fakeConn }

func (p fakePool) For(context.Context, string, string) (db.Conn, error) { return p.conn, nil }

type fakeSources struct{ companyID string }

func (f fakeSources) GetByID(_ context.Context, id string) (*domain.DBConnection, error) {
	return &domain.DBConnection{ID: id, CompanyID: f.companyID, DBType: fakeDBType}, nil
}

type fakeMetrics struct {
	from, to time.Time
	res      *metric.Result
	err      error
}

func (f *fakeMetrics) Query(_ context.Context, _, _ string, from, to time.Time, _ metric.Comparison) (*metric.Result, error) {
	f.from, f.to = from, to
	return f.res, f.err
}

func stored(panels ...spec.Panel) *domain.Dashboard {
	return &domain.Dashboard{
		ID:        "dash-1",
		CompanyID: "co-1",
		SourceID:  "src-1",
		Spec: spec.Dashboard{
			SpecVersion: spec.Version,
			Title:       "Revenue",
			SourceID:    "src-1",
			TimeZone:    "Asia/Jakarta",
			Filters:     []spec.Filter{{Name: "period", Kind: spec.KindDateRange, Default: string(spec.PresetLastMonth)}},
			Panels:      panels,
		},
	}
}

func chart(id string) spec.Panel {
	return spec.Panel{
		ID: id, Viz: spec.VizBar, Layout: spec.Layout{W: 6, H: 4},
		SQL: `SELECT month, revenue FROM v WHERE d >= {{period_from}} AND d < {{period_to}}`,
		Map: spec.Mapping{Label: "month", Series: []string{"revenue"}},
	}
}

func at(y int, m time.Month, d int) func() time.Time {
	return func() time.Time { return time.Date(y, m, d, 9, 0, 0, 0, time.UTC) }
}

func TestResolveBindsTheWindowAndDrawsThePanel(t *testing.T) {
	conn := &fakeConn{answer: func(string, []any, int) (*db.QueryResult, error) {
		return &db.QueryResult{Columns: []string{"month", "revenue"},
			Rows:  []map[string]any{{"month": "Feb", "revenue": 10.0}},
			Count: 1}, nil
	}}
	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{conn}, nil).WithClock(at(2024, 3, 15))

	res, err := r.Resolve(context.Background(), "co-1", stored(chart("p1")), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Panels) != 1 || res.Panels[0].Error != "" {
		t.Fatalf("panel = %+v", res.Panels[0])
	}
	// The default preset resolved rather than a stored pair of dates.
	if res.Applied["period"] != "last_month" {
		t.Errorf("applied = %v", res.Applied)
	}
	if got := res.Windows["period"].From.Format(DateLayout); got != "2024-02-01" {
		t.Errorf("window from = %s", got)
	}
	if got := conn.maxSeen.Load(); got != MaxRowsChart {
		t.Errorf("maxRows = %d, want the chart cap %d", got, MaxRowsChart)
	}
}

// The failure mode a dashboard is judged on: one panel breaking must not blank
// the ones that answered.
func TestResolveIsolatesAFailingPanel(t *testing.T) {
	conn := &fakeConn{answer: func(sql string, _ []any, _ int) (*db.QueryResult, error) {
		if strings.Contains(sql, "broken") {
			return nil, errors.New("relation \"broken\" does not exist")
		}
		return &db.QueryResult{Columns: []string{"month", "revenue"},
			Rows:  []map[string]any{{"month": "Feb", "revenue": 10.0}},
			Count: 1}, nil
	}}
	bad := chart("p2")
	bad.SQL = `SELECT month, revenue FROM broken WHERE d >= {{period_from}} AND d < {{period_to}}`

	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{conn}, nil).WithClock(at(2024, 3, 15))
	res, err := r.Resolve(context.Background(), "co-1", stored(chart("p1"), bad), nil)
	if err != nil {
		t.Fatalf("Resolve must succeed with a failing panel: %v", err)
	}
	if res.Panels[0].Error != "" {
		t.Errorf("the good panel carries an error: %s", res.Panels[0].Error)
	}
	if res.Panels[1].Error == "" {
		t.Error("the broken panel must carry its own error")
	}
	if res.Panels[1].PanelID != "p2" {
		t.Errorf("panels came back in the wrong order: %+v", res.Panels[1])
	}
}

// Row caps are per viz, and a table's is not a chart's.
func TestResolveCapsRowsPerViz(t *testing.T) {
	conn := &fakeConn{answer: func(string, []any, int) (*db.QueryResult, error) {
		return &db.QueryResult{Columns: []string{"a"}, Rows: []map[string]any{{"a": 1}}, Count: 1}, nil
	}}
	table := spec.Panel{ID: "t", Viz: spec.VizTable, Layout: spec.Layout{W: 12, H: 4},
		SQL: `SELECT a FROM v WHERE d >= {{period_from}} AND d < {{period_to}}`}
	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{conn}, nil).WithClock(at(2024, 3, 15))
	if _, err := r.Resolve(context.Background(), "co-1", stored(table), nil); err != nil {
		t.Fatal(err)
	}
	if got := conn.maxSeen.Load(); got != MaxRowsTable {
		t.Errorf("maxRows = %d, want the table cap %d", got, MaxRowsTable)
	}
}

// The stored spec is not trusted because it passed once: rows are edited by
// later releases and restored from backups, and after T-D13 this runs for a
// stranger holding a share link.
func TestResolveValidatesTheStoredSQLAgain(t *testing.T) {
	conn := &fakeConn{answer: func(string, []any, int) (*db.QueryResult, error) {
		t.Error("a refused statement must never reach the warehouse")
		return nil, nil
	}}
	tampered := chart("p1")
	tampered.SQL = `SELECT month, revenue FROM v WHERE d >= {{period_from}} AND d < {{period_to}}; DROP TABLE v`

	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{conn}, nil).WithClock(at(2024, 3, 15))
	res, err := r.Resolve(context.Background(), "co-1", stored(tampered), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Panels[0].Error == "" {
		t.Error("a spec edited underneath us must be refused at resolve")
	}
	if conn.calls.Load() != 0 {
		t.Error("nothing should have been executed")
	}
}

// A row read for one company must not resolve for another, whatever the caller
// believes. Same check, same reasoning, as MetricService.evaluate.
func TestResolveRefusesAMisScopedDashboard(t *testing.T) {
	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{&fakeConn{}}, nil)
	_, err := r.Resolve(context.Background(), "co-2", stored(chart("p1")), nil)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveMetricPanelUsesTheDeclaredWindow(t *testing.T) {
	metrics := &fakeMetrics{res: &metric.Result{Primary: metric.Evaluation{Value: 42}}}
	kpi := spec.Panel{ID: "k", Viz: spec.VizKPI, Layout: spec.Layout{W: 3, H: 2}, MetricKey: "revenue"}

	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{&fakeConn{}}, metrics).WithClock(at(2024, 3, 15))
	res, err := r.Resolve(context.Background(), "co-1", stored(kpi), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Panels[0].Value == nil || *res.Panels[0].Value != 42 {
		t.Fatalf("kpi = %+v", res.Panels[0])
	}
	if got := metrics.from.Format(DateLayout); got != "2024-02-01" {
		t.Errorf("the metric ran over %s, want the dashboard's own window", got)
	}
}

// The registry's distinction, carried through rather than flattened: no rows is
// not zero, and a KPI tile printing 0 for it is the fabrication 039 exists to
// stop.
func TestResolveMetricPanelKeepsEmptyDistinctFromZero(t *testing.T) {
	metrics := &fakeMetrics{res: &metric.Result{Primary: metric.Evaluation{Empty: true}}}
	kpi := spec.Panel{ID: "k", Viz: spec.VizKPI, Layout: spec.Layout{W: 3, H: 2}, MetricKey: "revenue"}

	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{&fakeConn{}}, metrics).WithClock(at(2024, 3, 15))
	res, err := r.Resolve(context.Background(), "co-1", stored(kpi), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Panels[0].Value != nil {
		t.Errorf("an empty metric must leave the value unset, got %v", *res.Panels[0].Value)
	}
	if res.Panels[0].Note == "" {
		t.Error("an empty metric must say so")
	}
}

// A bad filter value is the whole request's problem, not one panel's: it would
// fail identically for every panel and every viewer.
func TestResolveRefusesABadFilterValue(t *testing.T) {
	r := NewResolver(fakeSources{companyID: "co-1"}, fakePool{&fakeConn{}}, nil).WithClock(at(2024, 3, 15))
	_, err := r.Resolve(context.Background(), "co-1", stored(chart("p1")),
		map[string]string{"period_from": "yesterday", "period_to": "2024-01-31"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}
