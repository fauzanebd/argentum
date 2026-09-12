package app

import (
	"context"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// defaultCoverageDays is the window the panel opens on. Thirty days rather than
// seven: a registry accumulates over months, and a week of a quiet tenant is a
// handful of turns whose percentage swings on one question.
const defaultCoverageDays = 30

// maxCoverageDays bounds what a caller may ask for. The query is a GROUP BY over
// a company's whole audit log, and the index it uses is
// (company_id, created_at DESC) — so an unbounded window is a full scan of one
// tenant's history on a route anybody with an admin session can call.
const maxCoverageDays = 365

// MetricCoverageService reports whether a tenant's answers stand on defined
// metrics (T-F4).
type MetricCoverageService struct {
	repo domain.MetricCoverageReader
	now  func() time.Time
}

func NewMetricCoverageService(repo domain.MetricCoverageReader) *MetricCoverageService {
	return &MetricCoverageService{repo: repo, now: time.Now}
}

// CoverageReport is the whole panel in one read: the counts and the list of
// what to define next. One call rather than two, because the list is only
// meaningful beside the number it explains.
//
// The wire shape is handlers.MetricCoverageResponse, not this — `wire.go` is
// where a response that is neither an entity nor an event lives, so that
// `packages/api-types` generates it. This one is the service's own return.
type CoverageReport struct {
	Coverage domain.MetricCoverage
	AdHocTop []domain.AdHocQuestion
	Days     int
}

// ForCompany reads the window and assembles the report.
//
// **The ad-hoc list is best-effort.** If it fails, the counts are still
// returned — ChatRunner.companyContext's rule, that context makes an answer
// better and is never what makes one possible. A panel that shows the number
// and no list is useful; a panel that shows an error because the second query
// timed out is not.
func (s *MetricCoverageService) ForCompany(ctx context.Context, companyID string, days int) (CoverageReport, error) {
	days = clampQualityWindow(days)
	since := s.now().AddDate(0, 0, -days)

	cov, err := s.repo.Coverage(ctx, companyID, since)
	if err != nil {
		return CoverageReport{}, err
	}
	rep := CoverageReport{Coverage: cov, AdHocTop: []domain.AdHocQuestion{}, Days: days}
	// Only worth asking when there is something to list. A tenant at 100%
	// coverage should not pay for a second query to be told so.
	if cov.AdHoc > 0 {
		if top, err := s.repo.TopAdHocQuestions(ctx, companyID, since, 10); err == nil {
			rep.AdHocTop = top
		}
	}
	return rep, nil
}
