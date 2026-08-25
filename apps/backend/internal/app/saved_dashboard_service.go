package app

import (
	"context"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SavedDashboardService reads the `saved_dashboards` rows left behind by the
// Metabase era (T-D15).
//
// **It no longer talks to Metabase, because there is no Metabase.** What
// remains is the read path the roadmap asked be kept through the deprecation
// window: these rows are pointers at objects in a system this deployment no
// longer runs, and an archived list that still renders is how a tenant sees
// what they used to have.
//
// Deleting a row now removes the pointer and nothing else. There is no remote
// object left to remove, and saying that here is the difference between a
// delete that quietly does half of what its name implies and one whose scope
// somebody wrote down.
type SavedDashboardService struct {
	repo domain.SavedDashboardRepository
}

func NewSavedDashboardService(repo domain.SavedDashboardRepository) *SavedDashboardService {
	return &SavedDashboardService{repo: repo}
}

// Save records a dashboard pointer.
func (s *SavedDashboardService) Save(ctx context.Context, d *domain.SavedDashboard) error {
	return s.repo.Create(ctx, d)
}

// List returns all saved dashboards for a company.
func (s *SavedDashboardService) List(ctx context.Context, companyID string) ([]*domain.SavedDashboard, error) {
	return s.repo.ListByCompany(ctx, companyID)
}

// Delete removes one pointer, company-scoped.
func (s *SavedDashboardService) Delete(ctx context.Context, companyID, id string) error {
	d, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if d.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	return s.repo.Delete(ctx, id)
}

// DeleteByThread removes every pointer belonging to one thread.
func (s *SavedDashboardService) DeleteByThread(ctx context.Context, companyID, threadID string) error {
	list, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return err
	}
	for _, d := range list {
		if d.ThreadID != threadID {
			continue
		}
		if err := s.repo.Delete(ctx, d.ID); err != nil {
			return err
		}
	}
	return nil
}
