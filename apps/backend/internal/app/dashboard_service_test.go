package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
)

const dashTestDB = "dashboard_service_test_db"

type dashDialect struct{}

func (dashDialect) Type() string                                { return dashTestDB }
func (dashDialect) StatementTimeoutPragma(time.Duration) string { return "" }
func (dashDialect) ReadOnlyPragma() string                      { return "" }
func (dashDialect) QuoteIdentifier(n string) string             { return `"` + n + `"` }
func (dashDialect) Placeholder(n int) string                    { return "$" + strconv.Itoa(n) }

type dashDriver struct{}

func (dashDriver) Type() string        { return dashTestDB }
func (dashDriver) Dialect() db.Dialect { return dashDialect{} }
func (dashDriver) Open(context.Context, string) (db.Conn, error) {
	return nil, errors.New("not opened in tests")
}

func init() { db.Register(dashDriver{}) }

type dashConn struct {
	db.Conn
	answer func(sql string) (*db.QueryResult, error)
}

func (c dashConn) ExecuteReadOnlyParams(_ context.Context, sql string, _ []any, _ int) (*db.QueryResult, error) {
	return c.answer(sql)
}

type dashPool struct{ conn dashConn }

func (p dashPool) For(context.Context, string, string) (db.Conn, error) { return p.conn, nil }

// dashConns is both the resolver's SourceLookup and the service's connection
// repository, so one fake answers "does this company own the source?" the same
// way in both places.
type dashConns struct{ owned map[string]string } // sourceID -> companyID

func (c dashConns) GetByID(_ context.Context, id string) (*domain.DBConnection, error) {
	co, ok := c.owned[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.DBConnection{ID: id, CompanyID: co, DBType: dashTestDB}, nil
}

func (c dashConns) ListByCompany(_ context.Context, companyID string) ([]*domain.DBConnection, error) {
	var out []*domain.DBConnection
	for id, co := range c.owned {
		if co == companyID {
			out = append(out, &domain.DBConnection{ID: id, CompanyID: co, DBType: dashTestDB})
		}
	}
	return out, nil
}

func (c dashConns) Create(context.Context, *domain.DBConnection) error { return nil }
func (c dashConns) GetDefaultForCompany(context.Context, string) (*domain.DBConnection, error) {
	return nil, domain.ErrNotFound
}
func (c dashConns) Update(context.Context, *domain.DBConnection) error { return nil }
func (c dashConns) Delete(context.Context, string) error               { return nil }
func (c dashConns) SetDefault(context.Context, string, string) error   { return nil }

type dashRepo struct {
	rows    map[string]*domain.Dashboard
	creates int
}

func newDashRepo() *dashRepo { return &dashRepo{rows: map[string]*domain.Dashboard{}} }

func (r *dashRepo) Create(_ context.Context, d *domain.Dashboard) error {
	r.creates++
	d.ID = "dash-" + strconv.Itoa(r.creates)
	r.rows[d.ID] = d
	return nil
}

func (r *dashRepo) Update(_ context.Context, d *domain.Dashboard) error {
	cur, ok := r.rows[d.ID]
	if !ok || cur.CompanyID != d.CompanyID {
		return domain.ErrNotFound
	}
	r.rows[d.ID] = d
	return nil
}

func (r *dashRepo) GetByID(_ context.Context, companyID, id string) (*domain.Dashboard, error) {
	d, ok := r.rows[id]
	if !ok || d.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

func (r *dashRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.Dashboard, error) {
	var out []*domain.Dashboard
	for _, d := range r.rows {
		if d.CompanyID == companyID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *dashRepo) Delete(_ context.Context, companyID, id string) error {
	d, ok := r.rows[id]
	if !ok || d.CompanyID != companyID {
		return domain.ErrNotFound
	}
	delete(r.rows, id)
	return nil
}

func dashService(t *testing.T, answer func(sql string) (*db.QueryResult, error)) (*DashboardService, *dashRepo) {
	t.Helper()
	repo := newDashRepo()
	conns := dashConns{owned: map[string]string{"src-1": "co-1", "src-other": "co-2"}}
	resolver := dashboard.NewResolver(conns, dashPool{dashConn{answer: answer}}, nil).
		WithClock(func() time.Time { return time.Date(2024, 3, 15, 9, 0, 0, 0, time.UTC) })
	return NewDashboardService(repo, conns, resolver), repo
}

func input(panels ...spec.Panel) DashboardInput {
	return DashboardInput{
		Title: "Revenue",
		Spec: spec.Dashboard{
			Title:    "Revenue",
			SourceID: "src-1",
			TimeZone: "Asia/Jakarta",
			Filters:  []spec.Filter{{Name: "period", Kind: spec.KindDateRange, Default: string(spec.PresetLastMonth)}},
			Panels:   panels,
		},
	}
}

func okPanel(id string) spec.Panel {
	return spec.Panel{
		ID: id, Viz: spec.VizBar, Layout: spec.Layout{W: 6, H: 4},
		SQL: `SELECT month, revenue FROM v WHERE d >= {{period_from}} AND d < {{period_to}}`,
		Map: spec.Mapping{Label: "month", Series: []string{"revenue"}},
	}
}

func rows(string) (*db.QueryResult, error) {
	return &db.QueryResult{Columns: []string{"month", "revenue"},
		Rows: []map[string]any{{"month": "Feb", "revenue": 10.0}}, Count: 1}, nil
}

func TestCreateStoresAValidatedDashboard(t *testing.T) {
	svc, repo := dashService(t, rows)
	res, err := svc.Create(context.Background(), "co-1", "user-1", input(okPanel("p1")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %+v", res.Warnings)
	}
	if repo.creates != 1 || res.Dashboard.SpecVersion != spec.Version {
		t.Errorf("stored = %+v", res.Dashboard)
	}
	if res.Dashboard.CreatedBy == nil || *res.Dashboard.CreatedBy != "user-1" {
		t.Error("the author should be recorded")
	}
}

// Refuse on structure: the same mistake every time the dashboard loads, so it is
// refused where somebody can read the message.
func TestCreateRefusesAStructuralMistake(t *testing.T) {
	svc, repo := dashService(t, rows)
	bad := okPanel("p1")
	bad.MetricKey = "revenue" // both sources at once

	_, err := svc.Create(context.Background(), "co-1", "", input(bad))
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if repo.creates != 0 {
		t.Error("nothing should have been written")
	}
}

// Warn on execution: a dashboard is a dozen statements an agent wrote in a turn
// that is about to end, and losing eleven good panels because one hit a cold
// window is the worse failure.
func TestCreateSavesWithAWarningWhenAPanelCannotRun(t *testing.T) {
	svc, repo := dashService(t, func(sql string) (*db.QueryResult, error) {
		if strings.Contains(sql, "cold") {
			return nil, errors.New(`relation "cold" does not exist`)
		}
		return rows(sql)
	})
	cold := okPanel("p2")
	cold.SQL = `SELECT month, revenue FROM cold WHERE d >= {{period_from}} AND d < {{period_to}}`

	res, err := svc.Create(context.Background(), "co-1", "", input(okPanel("p1"), cold))
	if err != nil {
		t.Fatalf("Create must save: %v", err)
	}
	if repo.creates != 1 {
		t.Fatal("the dashboard should have been stored")
	}
	if len(res.Warnings) != 1 || res.Warnings[0].PanelID != "p2" {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
	// The window is in the message, because a panel that fails on a preset the
	// author never thought about reads as broken without it.
	if !strings.Contains(res.Warnings[0].Message, "2024-02-01") {
		t.Errorf("the warning should name the window it ran over, got %q", res.Warnings[0].Message)
	}
}

// A stored dashboard must not be a latent cross-tenant read waiting for a
// resolver bug.
func TestCreateRefusesASourceTheCompanyDoesNotOwn(t *testing.T) {
	svc, repo := dashService(t, rows)
	in := input(okPanel("p1"))
	in.Spec.SourceID = "src-other"

	_, err := svc.Create(context.Background(), "co-1", "", in)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if repo.creates != 0 {
		t.Error("nothing should have been written")
	}
}

// Isolation is in the WHERE clause, not in a comparison a caller can forget: a
// dashboard belonging to another company is not found rather than refused.
func TestGetAndUpdateAreCompanyScoped(t *testing.T) {
	svc, _ := dashService(t, rows)
	res, err := svc.Create(context.Background(), "co-1", "", input(okPanel("p1")))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Dashboard.ID

	if _, err := svc.Get(context.Background(), "co-2", id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get for another company = %v, want ErrNotFound", err)
	}
	if _, err := svc.Update(context.Background(), "co-2", id, input(okPanel("p1"))); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update for another company = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(context.Background(), "co-2", id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete for another company = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(context.Background(), "co-1", id); err != nil {
		t.Errorf("the owner must still read it: %v", err)
	}
}
