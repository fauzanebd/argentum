package app

import (
	"context"
	"testing"
	"time"

	// Registers the postgres driver so db.Get("postgres").Dialect().Placeholder
	// works — the service resolves the dialect from the source's db_type.
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
)

// --- fakes -----------------------------------------------------------------

type fakeMetricRepo struct {
	byKey map[string]*domain.MetricDefinition
}

func (r *fakeMetricRepo) Create(context.Context, *domain.MetricDefinition) error { return nil }
func (r *fakeMetricRepo) GetByID(context.Context, string, string) (*domain.MetricDefinition, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeMetricRepo) GetByKey(_ context.Context, _, key string) (*domain.MetricDefinition, error) {
	if m, ok := r.byKey[key]; ok {
		return m, nil
	}
	return nil, domain.ErrNotFound
}
func (r *fakeMetricRepo) ListByCompany(context.Context, string) ([]*domain.MetricDefinition, error) {
	return nil, nil
}
func (r *fakeMetricRepo) ListEnabled(context.Context, string) ([]*domain.MetricDefinition, error) {
	return nil, nil
}
func (r *fakeMetricRepo) Update(context.Context, *domain.MetricDefinition) error { return nil }
func (r *fakeMetricRepo) Delete(context.Context, string, string) error           { return nil }

type fakeSource struct{}

func (fakeSource) GetByID(_ context.Context, id string) (*domain.DBConnection, error) {
	return &domain.DBConnection{ID: id, CompanyID: "co-1", DBType: db.Postgres}, nil
}

// fakeConn records the last query it ran and returns a scripted result.
type fakeConn struct {
	gotSQL  string
	gotArgs []any
	result  *db.QueryResult
	err     error
}

func (c *fakeConn) ExecuteReadOnly(context.Context, string, int) (*db.QueryResult, error) {
	return c.result, c.err
}
func (c *fakeConn) ExecuteReadOnlyParams(_ context.Context, sql string, args []any, _ int) (*db.QueryResult, error) {
	c.gotSQL, c.gotArgs = sql, args
	return c.result, c.err
}
func (c *fakeConn) ExtractSchema(context.Context) (*db.SchemaMetadata, error) { return nil, nil }
func (c *fakeConn) Ping(context.Context) error                                { return nil }
func (c *fakeConn) Close() error                                              { return nil }

type fakePool struct{ conn *fakeConn }

func (p *fakePool) For(context.Context, string, string) (db.Conn, error) { return p.conn, nil }

func oneRow(col string, v any) *db.QueryResult {
	return &db.QueryResult{Columns: []string{col}, Rows: []map[string]any{{col: v}}, Count: 1}
}

func metricFixture() *domain.MetricDefinition {
	return &domain.MetricDefinition{
		ID: "m1", CompanyID: "co-1", SourceID: "src-1", Key: "revenue", Label: "Revenue",
		SQLTemplate: `SELECT sum(total) AS v FROM orders WHERE d >= {{from}} AND d < {{to}}`,
		ValueColumn: "v", Grain: domain.MetricGrainMonth, Unit: domain.MetricUnitCurrency,
		Currency: "IDR", HigherIsBetter: true, Enabled: true,
	}
}

func newService(conn *fakeConn, m *domain.MetricDefinition) *MetricService {
	return NewMetricService(
		&fakeMetricRepo{byKey: map[string]*domain.MetricDefinition{m.Key: m}},
		fakeSource{}, &fakePool{conn: conn},
	)
}

func jan(day int) time.Time { return time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC) }

// --- tests -----------------------------------------------------------------

// The value is bound, and the number comes straight out of the single row.
func TestQueryBindsWindowAndReadsTheValue(t *testing.T) {
	conn := &fakeConn{result: oneRow("v", int64(500))}
	svc := newService(conn, metricFixture())

	res, err := svc.Query(context.Background(), "co-1", "revenue", jan(1), jan(31), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Value != 500 {
		t.Errorf("value = %v, want 500", res.Primary.Value)
	}
	// The window is in args, not in the SQL.
	if len(conn.gotArgs) != 2 || conn.gotArgs[0] != jan(1) || conn.gotArgs[1] != jan(31) {
		t.Errorf("args = %v, want the bound window", conn.gotArgs)
	}
}

// A metric that returns more than one row is a broken definition, not a number.
func TestQueryRejectsMoreThanOneRow(t *testing.T) {
	conn := &fakeConn{result: &db.QueryResult{Columns: []string{"v"}, Rows: []map[string]any{{"v": 1}, {"v": 2}}, Count: 2}}
	if _, err := newService(conn, metricFixture()).Query(context.Background(), "co-1", "revenue", jan(1), jan(31), ""); err == nil {
		t.Error("a two-row result must be refused")
	}
}

// value_column must name a real column, and it must be numeric — a null is an
// error, not a silent zero.
func TestQueryRejectsMissingColumnAndNonNumeric(t *testing.T) {
	missing := &fakeConn{result: oneRow("other", 1)}
	if _, err := newService(missing, metricFixture()).Query(context.Background(), "co-1", "revenue", jan(1), jan(31), ""); err == nil {
		t.Error("a result without value_column must be refused")
	}
	null := &fakeConn{result: oneRow("v", nil)}
	if _, err := newService(null, metricFixture()).Query(context.Background(), "co-1", "revenue", jan(1), jan(31), ""); err == nil {
		t.Error("a null value must be refused, not read as 0")
	}
	text := &fakeConn{result: oneRow("v", "not a number")}
	if _, err := newService(text, metricFixture()).Query(context.Background(), "co-1", "revenue", jan(1), jan(31), ""); err == nil {
		t.Error("a non-numeric value must be refused")
	}
}

// A byte/string numeric (how some drivers hand back DECIMAL) is coerced.
func TestQueryCoercesStringNumerics(t *testing.T) {
	conn := &fakeConn{result: oneRow("v", []byte("1234.5"))}
	res, err := newService(conn, metricFixture()).Query(context.Background(), "co-1", "revenue", jan(1), jan(31), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Primary.Value != 1234.5 {
		t.Errorf("value = %v, want 1234.5", res.Primary.Value)
	}
}

// compare_to runs a second evaluation and returns the delta.
func TestQueryComputesTheDelta(t *testing.T) {
	// The fake conn returns the same value each call; primary 500, comparison
	// 500 → delta 0. We assert the comparison ran and the delta is present.
	conn := &fakeConn{result: oneRow("v", int64(500))}
	res, err := newService(conn, metricFixture()).Query(
		context.Background(), "co-1", "revenue", jan(1), jan(31), metric.ComparePreviousPeriod)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Comparison == nil || res.Delta == nil {
		t.Fatalf("comparison/delta missing: %+v", res)
	}
	if *res.Delta != 0 {
		t.Errorf("delta = %v, want 0 (equal values)", *res.Delta)
	}
	// The comparison window is the preceding equal-length period.
	if !res.Comparison.To.Equal(jan(1)) {
		t.Errorf("comparison.To = %v, want the primary's start", res.Comparison.To)
	}
}

// An unknown key is a recoverable tool result, not an error the turn dies on —
// the service returns ErrNotFound and the tool lists the real keys.
func TestQueryUnknownKeyIsNotFound(t *testing.T) {
	conn := &fakeConn{result: oneRow("v", 1)}
	_, err := newService(conn, metricFixture()).Query(context.Background(), "co-1", "nope", jan(1), jan(31), "")
	if err == nil {
		t.Fatal("want an error for an unknown key")
	}
}
