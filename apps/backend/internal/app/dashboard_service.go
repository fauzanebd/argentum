package app

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metabase"
)

// DashboardService manages persisted Metabase dashboards.
type DashboardService struct {
	repo domain.DashboardRepository
	mb   *metabase.Client
}

func NewDashboardService(repo domain.DashboardRepository, mb *metabase.Client) *DashboardService {
	return &DashboardService{repo: repo, mb: mb}
}

// Save persists a newly created dashboard.
func (s *DashboardService) Save(ctx context.Context, d *domain.SavedDashboard) error {
	return s.repo.Create(ctx, d)
}

// List returns all dashboards for a company.
func (s *DashboardService) List(ctx context.Context, companyID string) ([]*domain.SavedDashboard, error) {
	return s.repo.ListByCompany(ctx, companyID)
}

// Delete removes a dashboard from Metabase and the local DB.
func (s *DashboardService) Delete(ctx context.Context, companyID, id string) error {
	d, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if d.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	if s.mb != nil {
		if err := s.mb.DeleteDashboard(ctx, d.MetabaseDashboardID); err != nil {
			logrus.WithError(err).WithField("dashboard_id", d.MetabaseDashboardID).Warn("metabase dashboard delete failed")
		}
	}
	return s.repo.Delete(ctx, id)
}

// DeleteByThread removes all dashboards belonging to a thread from Metabase and DB.
func (s *DashboardService) DeleteByThread(ctx context.Context, companyID, threadID string) error {
	list, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return err
	}
	for _, d := range list {
		if d.ThreadID != threadID {
			continue
		}
		if s.mb != nil {
			if err := s.mb.DeleteDashboard(ctx, d.MetabaseDashboardID); err != nil {
				logrus.WithError(err).WithField("dashboard_id", d.MetabaseDashboardID).Warn("metabase dashboard delete failed")
			}
		}
	}
	return s.repo.DeleteByThread(ctx, threadID)
}
