package app

import (
	"context"
	"time"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
)

// clampQualityWindow applies the default and the ceiling to a requested window.
//
// Shared by every /quality panel (T-F4, T-W3) because they sit one above the
// other on one screen, and two halves of a page that quietly read different
// months would be comparing nothing.
func clampQualityWindow(days int) int {
	if days <= 0 {
		return defaultCoverageDays
	}
	if days > maxCoverageDays {
		return maxCoverageDays
	}
	return days
}

// DerivedFiguresService reports how often a tenant's answers stated a figure no
// tool returned, and how often `compute` was called and was not enough (T-W3).
type DerivedFiguresService struct {
	repo domain.DerivedFiguresReader
	now  func() time.Time
}

func NewDerivedFiguresService(repo domain.DerivedFiguresReader) *DerivedFiguresService {
	return &DerivedFiguresService{repo: repo, now: time.Now}
}

// DerivedFiguresReport is the service's return. The wire shape is
// handlers.DerivedFiguresResponse, for CoverageReport's reason.
type DerivedFiguresReport struct {
	Figures domain.DerivedFigures
	Days    int
}

// ForCompany reads the window and tallies it.
//
// **The data tools are agentbudget's list, not a copy.** "A turn that asked the
// data something" has to mean what "a tool whose result grounds a figure" means,
// or the panel counts a turn as answered that the grounding check never had
// evidence for — and the day `run_program` joins that map, it joins this query
// without anybody remembering to add it.
func (s *DerivedFiguresService) ForCompany(ctx context.Context, companyID string, days int) (DerivedFiguresReport, error) {
	days = clampQualityWindow(days)
	since := s.now().AddDate(0, 0, -days)

	shapes, err := s.repo.TurnShapes(ctx, companyID, since, agentbudget.DataTools())
	if err != nil {
		return DerivedFiguresReport{}, err
	}
	return DerivedFiguresReport{Figures: domain.TallyDerivedFigures(since, shapes), Days: days}, nil
}
