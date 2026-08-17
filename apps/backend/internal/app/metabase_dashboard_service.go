package app

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metabase"
)

// MetabaseDashboardService manages dashboards that live in Metabase and are
// recorded here as pointers (the create_dashboard tool, saved_dashboards).
//
// Named for the system it depends on rather than for what it does, because that
// is the honest description and because T-D15 deletes it: every reference to
// this type is a site the decommission has to visit, and a grep for "Metabase"
// should find all of them. DashboardService (dashboard_service.go) is the native
// replacement.
type MetabaseDashboardService struct {
	repo domain.SavedDashboardRepository
	mb   *metabase.Client
}

func NewMetabaseDashboardService(repo domain.SavedDashboardRepository, mb *metabase.Client) *MetabaseDashboardService {
	return &MetabaseDashboardService{repo: repo, mb: mb}
}

// Save persists a newly created dashboard.
func (s *MetabaseDashboardService) Save(ctx context.Context, d *domain.SavedDashboard) error {
	return s.repo.Create(ctx, d)
}

// List returns all dashboards for a company.
func (s *MetabaseDashboardService) List(ctx context.Context, companyID string) ([]*domain.SavedDashboard, error) {
	return s.repo.ListByCompany(ctx, companyID)
}

// Delete removes a dashboard from Metabase and the local DB.
func (s *MetabaseDashboardService) Delete(ctx context.Context, companyID, id string) error {
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
func (s *MetabaseDashboardService) DeleteByThread(ctx context.Context, companyID, threadID string) error {
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
