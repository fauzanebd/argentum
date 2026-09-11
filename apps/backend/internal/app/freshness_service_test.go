package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/freshness"
)

type fakeSourceStore struct {
	src     *domain.DBConnection
	err     error
	written []domain.SourceFreshness
}

func (f *fakeSourceStore) GetByID(context.Context, string) (*domain.DBConnection, error) {
	return f.src, f.err
}

func (f *fakeSourceStore) SetFreshness(_ context.Context, _ string, sf domain.SourceFreshness) error {
	f.written = append(f.written, sf)
	return nil
}

type fakeFreshConn struct {
	at    time.Time
	err   error
	calls *int
}

func (c *fakeFreshConn) ExecuteReadOnly(context.Context, string, int) (*db.QueryResult, error) {
	return nil, errors.New("not used")
}
func (c *fakeFreshConn) ExecuteReadOnlyParams(context.Context, string, []any, int) (*db.QueryResult, error) {
	*c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &db.QueryResult{
		Columns: []string{"loaded_at"},
		Rows:    []map[string]interface{}{{"loaded_at": c.at}},
		Count:   1,
	}, nil
}
func (c *fakeFreshConn) ExtractSchema(context.Context) (*db.SchemaMetadata, error) {
	return nil, errors.New("not used")
}
func (c *fakeFreshConn) Ping(context.Context) error { return nil }
func (c *fakeFreshConn) Close() error               { return nil }

type fakeFreshPool struct {
	conn *fakeFreshConn
	err  error
}

func (p *fakeFreshPool) For(context.Context, string, string) (db.Conn, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.conn, nil
}

const (
	freshCompany = "co-1"
	freshSource  = "src-1"
)

func configuredSource() *domain.DBConnection {
	return &domain.DBConnection{
		ID:        freshSource,
		CompanyID: freshCompany,
		Freshness: domain.SourceFreshness{
			SQL:            "SELECT max(loaded_at) FROM etl_runs",
			WarnAfterMins:  120,
			StaleAfterMins: 1440,
		},
	}
}

func newFreshService(t *testing.T, src *domain.DBConnection, conn *fakeFreshConn, now time.Time) (*FreshnessService, *fakeSourceStore) {
	t.Helper()
	store := &fakeSourceStore{src: src}
	s := NewFreshnessService(store, store, &fakeFreshPool{conn: conn}, time.Minute)
	s.now = func() time.Time { return now }
	return s, store
}

func TestForClassifiesWhatTheSourceReports(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	calls := 0
	s, _ := newFreshService(t, configuredSource(), &fakeFreshConn{at: now.Add(-30 * time.Hour), calls: &calls}, now)

	got := s.For(context.Background(), freshCompany, freshSource)
	if got.Verdict != freshness.Stale {
		t.Fatalf("verdict = %q; want %q", got.Verdict, freshness.Stale)
	}
	if got.Note == "" {
		t.Error("a stale verdict reached a reader with nothing to say")
	}
}

// The acceptance item: two probes inside the TTL run one query.
func TestTheProbeIsCachedForTheTTL(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	calls := 0
	s, _ := newFreshService(t, configuredSource(), &fakeFreshConn{at: now, calls: &calls}, now)

	for i := 0; i < 5; i++ {
		s.For(context.Background(), freshCompany, freshSource)
	}
	if calls != 1 {
		t.Fatalf("probes = %d; want 1 — a turn that runs five queries against one source pays for one probe", calls)
	}

	s.now = func() time.Time { return now.Add(2 * time.Minute) }
	s.For(context.Background(), freshCompany, freshSource)
	if calls != 2 {
		t.Errorf("probes after the TTL = %d; want 2", calls)
	}
}

// A broken expression must not be re-probed on every tool call: the worst case
// is a warehouse that is down, where each probe waits out a connection timeout.
func TestAFailedProbeIsCachedToo(t *testing.T) {
	now := time.Now()
	calls := 0
	s, _ := newFreshService(t, configuredSource(), &fakeFreshConn{err: errors.New("connection refused"), calls: &calls}, now)

	for i := 0; i < 4; i++ {
		if got := s.For(context.Background(), freshCompany, freshSource); got.Verdict != freshness.Unknown {
			t.Fatalf("verdict = %q; want %q", got.Verdict, freshness.Unknown)
		}
	}
	if calls != 1 {
		t.Errorf("probes = %d; want 1 — a broken source is probed once per TTL, not once per query", calls)
	}
}

func TestAnUnconfiguredSourceIsUnknownAndCostsNothing(t *testing.T) {
	now := time.Now()
	calls := 0
	src := &domain.DBConnection{ID: freshSource, CompanyID: freshCompany}
	s, _ := newFreshService(t, src, &fakeFreshConn{at: now, calls: &calls}, now)

	if got := s.For(context.Background(), freshCompany, freshSource); got.Verdict != freshness.Unknown {
		t.Errorf("verdict = %q; want %q", got.Verdict, freshness.Unknown)
	}
	if calls != 0 {
		t.Errorf("probes = %d; a source nobody configured must not be queried", calls)
	}
}

// Another tenant's source id reports unknown rather than that tenant's verdict.
func TestForRefusesToCrossATenant(t *testing.T) {
	now := time.Now()
	calls := 0
	src := configuredSource()
	src.CompanyID = "someone-else"
	s, _ := newFreshService(t, src, &fakeFreshConn{at: now, calls: &calls}, now)

	if got := s.For(context.Background(), freshCompany, freshSource); got.Verdict != freshness.Unknown {
		t.Errorf("verdict = %q; want %q", got.Verdict, freshness.Unknown)
	}
	if calls != 0 {
		t.Errorf("probes = %d; another tenant's source was queried", calls)
	}
}

// Everything the turn can hit reports unknown rather than failing, because a
// broken ETL must not be what stops an answer.
func TestForNeverFailsTheTurn(t *testing.T) {
	now := time.Now()
	cases := map[string]*FreshnessService{}

	store := &fakeSourceStore{err: errors.New("database is down")}
	s1 := NewFreshnessService(store, store, &fakeFreshPool{conn: &fakeFreshConn{calls: new(int)}}, time.Minute)
	cases["the source lookup failed"] = s1

	store2 := &fakeSourceStore{src: configuredSource()}
	s2 := NewFreshnessService(store2, store2, &fakeFreshPool{err: errors.New("dial timeout")}, time.Minute)
	cases["the source could not be reached"] = s2

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			s.now = func() time.Time { return now }
			if got := s.For(context.Background(), freshCompany, freshSource); got.Verdict != freshness.Unknown {
				t.Errorf("verdict = %q; want %q", got.Verdict, freshness.Unknown)
			}
		})
	}
}

func TestSetValidatesBeforeItStores(t *testing.T) {
	now := time.Now()
	s, store := newFreshService(t, configuredSource(), &fakeFreshConn{at: now, calls: new(int)}, now)

	err := s.Set(context.Background(), freshCompany, freshSource, domain.SourceFreshness{
		SQL: "DELETE FROM etl_runs", StaleAfterMins: 60,
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v; want ErrInvalidInput", err)
	}
	if len(store.written) != 0 {
		t.Errorf("wrote %d rows; a refused configuration must not be stored", len(store.written))
	}
}

func TestSetDropsTheCachedVerdict(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	calls := 0
	s, _ := newFreshService(t, configuredSource(), &fakeFreshConn{at: now.Add(-3 * time.Hour), calls: &calls}, now)

	if got := s.For(context.Background(), freshCompany, freshSource); got.Verdict != freshness.Warn {
		t.Fatalf("verdict = %q; want %q", got.Verdict, freshness.Warn)
	}
	if err := s.Set(context.Background(), freshCompany, freshSource, domain.SourceFreshness{
		SQL: "SELECT max(loaded_at) FROM etl_runs", StaleAfterMins: 60,
	}); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	// The stored source in the fake still carries the old thresholds, so what is
	// being checked here is that the probe ran again rather than being served
	// from a cache the new configuration should have emptied.
	s.For(context.Background(), freshCompany, freshSource)
	if calls != 2 {
		t.Errorf("probes = %d; want 2 — a threshold change must not wait out a TTL", calls)
	}
}

func TestSetIsANotFoundForAnotherTenantsSource(t *testing.T) {
	now := time.Now()
	src := configuredSource()
	src.CompanyID = "someone-else"
	s, store := newFreshService(t, src, &fakeFreshConn{at: now, calls: new(int)}, now)

	err := s.Set(context.Background(), freshCompany, freshSource, domain.SourceFreshness{
		SQL: "SELECT max(loaded_at) FROM etl_runs", StaleAfterMins: 60,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v; want ErrNotFound — never ErrForbidden, which is an existence oracle", err)
	}
	if len(store.written) != 0 {
		t.Error("another tenant's source was written to")
	}
}

func TestTestReportsTheProblemInsteadOfSwallowingIt(t *testing.T) {
	now := time.Now()
	s, store := newFreshService(t, configuredSource(), &fakeFreshConn{err: errors.New("relation \"etl_runs\" does not exist"), calls: new(int)}, now)

	rep, err := s.Test(context.Background(), freshCompany, freshSource, domain.SourceFreshness{
		SQL: "SELECT max(loaded_at) FROM etl_runs", StaleAfterMins: 60,
	})
	if err == nil {
		t.Fatal("Test() = nil; the admin pressing Test is exactly who needs the message")
	}
	if rep.Verdict != freshness.Unknown {
		t.Errorf("verdict = %q; want %q", rep.Verdict, freshness.Unknown)
	}
	if len(store.written) != 0 {
		t.Error("Test stored the configuration; it must not")
	}
}
