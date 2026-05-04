package domain

import (
	"context"
	"time"
)

// SavedDashboard is a persisted Metabase dashboard created by the agent.
type SavedDashboard struct {
	ID                  string    `json:"id"`
	CompanyID           string    `json:"company_id"`
	ThreadID            string    `json:"thread_id"`
	MetabaseDashboardID int       `json:"metabase_dashboard_id"`
	Name                string    `json:"name"`
	PublicURL           string    `json:"public_url"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// DashboardRepository is the persistence contract for saved dashboards.
type DashboardRepository interface {
	Create(ctx context.Context, d *SavedDashboard) error
	GetByID(ctx context.Context, id string) (*SavedDashboard, error)
	ListByCompany(ctx context.Context, companyID string) ([]*SavedDashboard, error)
	Delete(ctx context.Context, id string) error
	DeleteByThread(ctx context.Context, threadID string) error
}
