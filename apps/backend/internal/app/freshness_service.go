package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/freshness"
)

// FreshnessProber is the one question the tools ask about a source: how current
// is it. Declared here as a consumer interface so a tool can be handed a stub —
// and, more usefully, so a build that has not wired freshness at all hands them
// nil and every answer is `unknown`, which is the verdict that says nothing.
type FreshnessProber interface {
	For(ctx context.Context, companyID, sourceID string) freshness.Report
}

// freshnessPool is the slice of db.TenantConnPool this service needs.
type freshnessPool interface {
	For(ctx context.Context, companyID, sourceID string) (db.Conn, error)
}

// freshnessSourceReader is the slice of the connection repository it needs.
type freshnessSourceReader interface {
	GetByID(ctx context.Context, id string) (*domain.DBConnection, error)
}

// freshnessWriter is the repository's focused setter.
type freshnessWriter interface {
	SetFreshness(ctx context.Context, id string, f domain.SourceFreshness) error
}

type cachedReport struct {
	report  freshness.Report
	expires time.Time
}

// FreshnessService answers "when did this source last load" for the turn, and
// owns the tenant-facing configuration of it.
//
// **It never returns an error to a turn.** A source whose probe failed is
// `unknown`, and unknown says nothing to the user. An operator's ETL being
// broken must not be the thing that stops an answer — ChatRunner.companyContext's
// rule, that context makes an answer better and is never what makes one
// possible.
type FreshnessService struct {
	sources freshnessSourceReader
	writer  freshnessWriter
	pool    freshnessPool
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cachedReport
}

// NewFreshnessService builds the service. A non-positive ttl falls back to the
// default, because a zero TTL means one probe per tool call against a tenant's
// warehouse and nobody would choose that on purpose.
func NewFreshnessService(sources freshnessSourceReader, writer freshnessWriter, pool freshnessPool, ttl time.Duration) *FreshnessService {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &FreshnessService{
		sources: sources,
		writer:  writer,
		pool:    pool,
		ttl:     ttl,
		now:     time.Now,
		cache:   map[string]cachedReport{},
	}
}

// For reports how current a source is, from cache when it can.
//
// The cache holds failures as well as successes, deliberately. A source whose
// freshness expression is broken would otherwise be probed on every single tool
// call — the worst case being a warehouse that is down, where each probe waits
// for a connection timeout in front of a query that was going to fail anyway.
func (s *FreshnessService) For(ctx context.Context, companyID, sourceID string) freshness.Report {
	if s == nil || companyID == "" || sourceID == "" {
		return freshness.Report{Verdict: freshness.Unknown}
	}
	if r, ok := s.cached(sourceID); ok {
		return r
	}

	src, err := s.sources.GetByID(ctx, sourceID)
	if err != nil || src == nil || src.CompanyID != companyID {
		// Not logged at Warn: a source that vanished mid-turn, or one belonging
		// to another tenant, is a resolution problem the caller already has a
		// better error for.
		return s.remember(sourceID, freshness.Report{Verdict: freshness.Unknown})
	}
	cfg := configOf(src.Freshness)
	if !cfg.Configured() {
		return s.remember(sourceID, freshness.Report{Verdict: freshness.Unknown})
	}

	conn, err := s.pool.For(ctx, companyID, sourceID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "source_id": sourceID,
		}).Warn("freshness probe could not reach the source; reporting unknown")
		return s.remember(sourceID, freshness.Report{Verdict: freshness.Unknown})
	}

	rep, err := freshness.Probe(ctx, conn, cfg, s.now())
	if err != nil {
		// Once per TTL rather than once per call, which is the second reason the
		// failure is cached: a broken expression should produce one log line a
		// minute, not one per query in every turn.
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "source_id": sourceID,
		}).Warn("freshness probe did not return a timestamp; reporting unknown")
	}
	return s.remember(sourceID, rep)
}

// Test probes a candidate configuration without storing it — the Test-then-Save
// pairing T-06 built for metrics, for the same reason: a tenant should find out
// their expression returns three rows at save time, not from a watcher at 03:00.
//
// Unlike For, this one returns the error. An admin pressing Test is exactly the
// person who needs to read "returned 2 rows; it must return exactly one", and
// it is the only place the parsed instant is shown — which is how somebody in
// Jakarta discovers their naive timestamp is being read as UTC.
func (s *FreshnessService) Test(ctx context.Context, companyID, sourceID string, f domain.SourceFreshness) (freshness.Report, error) {
	cfg := configOf(f)
	if err := cfg.Validate(); err != nil {
		return freshness.Report{Verdict: freshness.Unknown}, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}
	src, err := s.sources.GetByID(ctx, sourceID)
	if err != nil || src == nil || src.CompanyID != companyID {
		return freshness.Report{Verdict: freshness.Unknown}, domain.ErrNotFound
	}
	if !cfg.Configured() {
		return freshness.Report{Verdict: freshness.Unknown}, nil
	}
	conn, err := s.pool.For(ctx, companyID, sourceID)
	if err != nil {
		return freshness.Report{Verdict: freshness.Unknown}, fmt.Errorf("reach source: %w", err)
	}
	return freshness.Probe(ctx, conn, cfg, s.now())
}

// Set validates and stores a source's freshness configuration, and drops the
// cached verdict so the next turn sees the new thresholds rather than the old
// ones for up to a TTL.
func (s *FreshnessService) Set(ctx context.Context, companyID, sourceID string, f domain.SourceFreshness) error {
	f.SQL = strings.TrimSpace(f.SQL)
	if err := configOf(f).Validate(); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}
	src, err := s.sources.GetByID(ctx, sourceID)
	if err != nil {
		return err
	}
	// 404 and not 403, for the reason RosterReader.GetByID established: a
	// refusal that distinguishes "not yours" from "does not exist" is an
	// existence oracle for another tenant's source ids.
	if src == nil || src.CompanyID != companyID {
		return domain.ErrNotFound
	}
	if err := s.writer.SetFreshness(ctx, sourceID, f); err != nil {
		return err
	}
	s.invalidate(sourceID)
	return nil
}

func (s *FreshnessService) cached(sourceID string) (freshness.Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[sourceID]
	if !ok || s.now().After(e.expires) {
		return freshness.Report{}, false
	}
	return e.report, true
}

func (s *FreshnessService) remember(sourceID string, r freshness.Report) freshness.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[sourceID] = cachedReport{report: r, expires: s.now().Add(s.ttl)}
	return r
}

func (s *FreshnessService) invalidate(sourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, sourceID)
}

// configOf converts the stored shape into the policy package's. It lives here
// rather than in either package so internal/domain stays free of dependencies
// and internal/freshness stays free of the database's storage decisions.
func configOf(f domain.SourceFreshness) freshness.Config {
	return freshness.Config{
		SQL:        f.SQL,
		WarnAfter:  time.Duration(f.WarnAfterMins) * time.Minute,
		StaleAfter: time.Duration(f.StaleAfterMins) * time.Minute,
	}
}
