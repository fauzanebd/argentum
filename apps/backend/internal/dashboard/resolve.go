package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// The caps a resolve runs under. Each is a number somebody will want to raise,
// so each says what it protects rather than what it allows.
const (
	// MaxRowsChart is far above run_sql's 100, which is tuned for what fits in an
	// LLM's context. A chart is read by a browser and 100 points is a fortnight
	// of daily data.
	MaxRowsChart = 2000
	// MaxRowsTable stops where a browser table wants pagination, which v1 does
	// not have.
	MaxRowsTable = 500
	// MaxRowsKPI is enough to tell "exactly one row" from "more than one" — the
	// same value and the same reason as metric_service.go, so a SQL-backed KPI
	// and a metric-backed one agree about what one row means.
	MaxRowsKPI = 2
	// DefaultPanelTimeout bounds the worst case for a viewer waiting on a dozen
	// panels. Override with DASHBOARD_PANEL_TIMEOUT.
	DefaultPanelTimeout = 15 * time.Second
	// DefaultConcurrency is how many panels touch the warehouse at once. Twelve
	// simultaneous connections into a customer's production replica is a load
	// pattern they did not agree to, and this is the number that decides it.
	DefaultConcurrency = 4
)

// ConnResolver is the half of the tenant pool this package needs.
// *db.TenantConnPool satisfies it.
type ConnResolver interface {
	For(ctx context.Context, companyID, sourceID string) (db.Conn, error)
}

// metaConnResolver is ConnResolver plus the connection's identity, which the
// panel cache needs and nothing else does (T-D8).
//
// An optional interface rather than a widened ConnResolver, so every existing
// implementation — including the fakes in this package's own tests — keeps
// compiling. *db.TenantConnPool satisfies it. A resolver that does not is
// simply not cached, which is the safe direction: caching without the
// connection version is the one thing this cache must never do.
type metaConnResolver interface {
	ForWithMeta(ctx context.Context, companyID, sourceID string) (db.Conn, db.ConnMeta, error)
}

// SourceLookup is the one read the resolver makes of the connection repo: a
// source by id, for its db_type (the dialect) and its owner.
type SourceLookup interface {
	GetByID(ctx context.Context, id string) (*domain.DBConnection, error)
}

// MetricQuerier is the metric registry as a dashboard sees it.
// *app.MetricService satisfies it.
type MetricQuerier interface {
	Query(ctx context.Context, companyID, key string, from, to time.Time, compare metric.Comparison) (*metric.Result, error)
}

// Resolver executes a stored dashboard against a tenant warehouse.
type Resolver struct {
	conns   SourceLookup
	pool    ConnResolver
	metrics MetricQuerier

	now         func() time.Time
	timeout     time.Duration
	concurrency int
	cache       *PanelCache
	log         QueryLogger
}

func NewResolver(conns SourceLookup, pool ConnResolver, metrics MetricQuerier) *Resolver {
	return &Resolver{
		conns:       conns,
		pool:        pool,
		metrics:     metrics,
		now:         time.Now,
		timeout:     DefaultPanelTimeout,
		concurrency: DefaultConcurrency,
	}
}

// WithPanelTimeout overrides the per-panel deadline (DASHBOARD_PANEL_TIMEOUT).
func (r *Resolver) WithPanelTimeout(d time.Duration) *Resolver {
	if d > 0 {
		r.timeout = d
	}
	return r
}

// WithCache turns on T-D8's panel cache. A nil cache leaves the resolver
// behaving exactly as it did before, which is what a deployment without Redis
// gets.
func (r *Resolver) WithCache(c *PanelCache) *Resolver {
	r.cache = c
	return r
}

// WithQueryLog turns on T-D9's log of what ran against the tenant warehouse.
func (r *Resolver) WithQueryLog(l QueryLogger) *Resolver {
	r.log = l
	return r
}

// WithClock injects a clock so preset windows are deterministic in tests.
func (r *Resolver) WithClock(now func() time.Time) *Resolver {
	if now != nil {
		r.now = now
	}
	return r
}

// Resolve binds the request's filters and runs every panel.
//
// A panel that fails fills its own Error and the resolve still succeeds. One
// timed-out panel must not blank the eleven that answered — that is the failure
// mode a dashboard is judged on, because it is the one an executive sees at
// 08:00 on a Monday.
func (r *Resolver) Resolve(ctx context.Context, companyID string, d *domain.Dashboard, req map[string]string) (*Result, error) {
	if d == nil {
		return nil, domain.ErrNotFound
	}
	if d.CompanyID != companyID {
		// The row was read company-scoped, so this should be impossible; refusing
		// rather than trusting keeps a mis-scoped read from running one tenant's
		// SQL on another's word. Same check, same reasoning, as
		// MetricService.evaluate.
		return nil, domain.ErrNotFound
	}
	params, err := Bind(&d.Spec, req, r.now())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	out := &Result{
		DashboardID: d.ID,
		Title:       d.Spec.Title,
		Applied:     params.Applied,
		Windows:     params.Windows,
		Panels:      make([]*spec.Resolved, len(d.Spec.Panels)),
		ResolvedAt:  r.now(),
	}

	sem := make(chan struct{}, r.concurrency)
	var wg sync.WaitGroup
	for i := range d.Spec.Panels {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				out.Panels[i] = failed(&d.Spec.Panels[i], ctx.Err())
				return
			}
			out.Panels[i] = r.resolvePanel(ctx, companyID, d, &d.Spec.Panels[i], params)
		}(i)
	}
	wg.Wait()
	return out, nil
}

func (r *Resolver) resolvePanel(ctx context.Context, companyID string, d *domain.Dashboard, p *spec.Panel, params *Params) *spec.Resolved {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	if p.MetricKey != "" {
		res, err := r.resolveMetricPanel(ctx, companyID, d, p, params)
		if err != nil {
			return failed(p, err)
		}
		return res
	}
	res, err := r.resolveSQLPanel(ctx, companyID, d, p, params)
	if err != nil {
		return failed(p, err)
	}
	return res
}

// resolveSQLPanel renders the panel's SQL with the request's bound values and
// runs it in a read-only transaction.
func (r *Resolver) resolveSQLPanel(ctx context.Context, companyID string, d *domain.Dashboard, p *spec.Panel, params *Params) (*spec.Resolved, error) {
	// Validated again here, not only at save. A stored spec is not trusted
	// because it passed once: rows are edited by later releases, restored from
	// backups, and — after T-D13 — resolved for a stranger holding a share link.
	// The check is cheap and the path it guards is the unauthenticated one.
	if err := sqlguard.ValidateStatement(p.SQL, d.Spec.DeclaredTokens()); err != nil {
		return nil, err
	}

	source, err := r.conns.GetByID(ctx, d.SourceID)
	if err != nil {
		return nil, fmt.Errorf("resolve dashboard source: %w", err)
	}
	if source.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	driver, err := db.Get(source.DBType)
	if err != nil {
		return nil, fmt.Errorf("dashboard source driver: %w", err)
	}
	sql, args, err := Render(p.SQL, driver.Dialect().Placeholder, params.Values)
	if err != nil {
		return nil, err
	}
	maxRows := maxRowsFor(p.Viz)

	// ForWithMeta where the pool offers it, because the connection's version is
	// part of the cache key and a cache without it can serve a disconnected
	// warehouse (T-D8). A resolver that cannot supply it still works and simply
	// is not cached.
	var (
		conn db.Conn
		meta db.ConnMeta
	)
	if mp, ok := r.pool.(metaConnResolver); ok {
		conn, meta, err = mp.ForWithMeta(ctx, companyID, d.SourceID)
	} else {
		conn, err = r.pool.For(ctx, companyID, d.SourceID)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to dashboard source: %w", err)
	}

	run := func() ([]byte, error) {
		started := r.now()
		res, execErr := conn.ExecuteReadOnlyParams(ctx, sql, args, maxRows)
		// Logged here rather than after Do returns, so the row is written once
		// per execution by construction: a cache hit and a collapsed wait never
		// reach this function, and an error still leaves a record of what was
		// attempted (T-D9).
		r.logQuery(ctx, d, p, sql, params, res, execErr, r.now().Sub(started))
		if execErr != nil {
			return nil, execErr
		}
		return json.Marshal(res)
	}

	var raw []byte
	if meta.Version != "" {
		key := SQLPanelKey(companyID, meta.SourceID, meta.Version, meta.DBType, sql, argsFingerprint(args), maxRows)
		raw, _, err = r.cache.Do(ctx, key, run)
	} else {
		raw, err = run()
	}
	if err != nil {
		return nil, err
	}

	var res db.QueryResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode cached panel result: %w", err)
	}
	// Projected after the cache, not before it. The cached value is the answer
	// the warehouse gave; the title, viz and format are the panel's, and an
	// author who renames a panel without touching its SQL must see the new name
	// on the next render rather than at the next TTL expiry.
	return spec.Project(p, &res)
}

// argsFingerprint renders bound parameters into the cache key. json.Marshal of
// []any is stable for the scalar types Render produces.
func argsFingerprint(args []any) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		// Unfingerprintable arguments must not collide with a different set, so
		// the key becomes unique and this panel simply is not shared.
		return fmt.Sprintf("unmarshalable:%d:%v", len(args), args)
	}
	return string(b)
}

// resolveMetricPanel reads a KPI from the registry instead of the warehouse
// directly, so the number on the dashboard is the number query_metric gives the
// same question in a chat thread.
//
// The window comes from the dashboard's first date_range filter. A metric is
// measured over a window by construction, and a dashboard that offers none has
// nothing to measure it over — which is a spec mistake with a clear message
// rather than a silently invented default.
func (r *Resolver) resolveMetricPanel(ctx context.Context, companyID string, d *domain.Dashboard, p *spec.Panel, params *Params) (*spec.Resolved, error) {
	if r.metrics == nil {
		return nil, errors.New("this deployment has no metric registry wired")
	}
	w, ok := firstWindow(&d.Spec, params)
	if !ok {
		return nil, fmt.Errorf("a metric panel needs a date_range filter to be measured over; this dashboard declares none")
	}
	// Keyed on the metric and the window rather than on rendered SQL, because
	// MetricService owns its own rendering and this package never sees the
	// statement (T-D8). No query-log row: the log records statements this
	// resolver ran, and a metric panel's statement is the registry's.
	var res *metric.Result
	raw, _, err := r.cache.Do(ctx, MetricPanelKey(companyID, p.MetricKey, w.From, w.To), func() ([]byte, error) {
		out, qErr := r.metrics.Query(ctx, companyID, p.MetricKey, w.From, w.To, "")
		if qErr != nil {
			return nil, qErr
		}
		return json.Marshal(out)
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode cached metric panel: %w", err)
	}

	out := &spec.Resolved{PanelID: p.ID, Title: p.Title, Viz: p.Viz, Fmt: p.Fmt, RowCount: 1}
	if res.Primary.Empty {
		// The registry's own distinction, carried through rather than flattened:
		// "no rows in this window" is not zero, and a KPI tile that prints 0 for
		// it is the fabrication 039 and T-Q9 both exist to stop.
		out.RowCount = 0
		out.Note = fmt.Sprintf("no data between %s and %s",
			w.From.Format(DateLayout), w.To.Format(DateLayout))
		return out, nil
	}
	v := res.Primary.Value
	out.Value = &v
	if res.Comparison != nil && !res.Comparison.Empty {
		c := res.Comparison.Value
		out.Comparison = &c
	}
	out.Delta, out.DeltaPct = res.Delta, res.DeltaPct
	return out, nil
}

// firstWindow returns the window of the dashboard's first declared date_range
// filter. First declared, not first bound: the spec's order is the author's
// order and it is stable across requests, where a map's is not.
func firstWindow(d *spec.Dashboard, params *Params) (spec.Window, bool) {
	for _, f := range d.Filters {
		if f.Kind != spec.KindDateRange {
			continue
		}
		if w, ok := params.Windows[f.Name]; ok {
			return w, true
		}
	}
	return spec.Window{}, false
}

func maxRowsFor(v spec.Viz) int {
	switch v {
	case spec.VizKPI:
		return MaxRowsKPI
	case spec.VizTable:
		return MaxRowsTable
	default:
		return MaxRowsChart
	}
}

// failed renders a panel's failure as the panel's own answer. The tile says what
// went wrong where the reader is looking, rather than the page saying nothing.
func failed(p *spec.Panel, err error) *spec.Resolved {
	return &spec.Resolved{PanelID: p.ID, Title: p.Title, Viz: p.Viz, Fmt: p.Fmt, Error: err.Error()}
}
