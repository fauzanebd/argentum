package domain

import (
	"context"
	"time"

	"github.com/fauzanebd/argentum/internal/dashboard/spec"
)

// Dashboard is a native dashboard: a stored spec Argentum executes itself
// (T-D5/T-D6), against a tenant connection it owns, through the audit path every
// other warehouse read goes down.
//
// ThreadID is provenance, not ownership — which conversation produced it, if one
// did. It does not cascade, because the dashboard somebody opens every Monday
// must not disappear when they tidy the chat thread that happened to create it.
type Dashboard struct {
	ID          string         `json:"id"`
	CompanyID   string         `json:"company_id"`
	ThreadID    *string        `json:"thread_id,omitempty"`
	SourceID    string         `json:"source_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Spec        spec.Dashboard `json:"spec"`
	SpecVersion int            `json:"spec_version"`
	RefreshSecs *int           `json:"refresh_secs,omitempty"`
	CreatedBy   *string        `json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// DashboardRepository is the persistence contract for native dashboards.
//
// **Every method takes companyID beside the id.** That is not ceremony: the
// repository it replaces had GetByID(ctx, id) and compared ownership in Go
// afterwards, so tenant isolation depended on every caller remembering to write
// the comparison. Here it is in the WHERE clause, where it cannot be forgotten,
// and a row belonging to another company is indistinguishable from one that does
// not exist — which is also the right answer to give the caller.
// domain.MetricRepository is the shape this copies.
type DashboardRepository interface {
	Create(ctx context.Context, d *Dashboard) error
	Update(ctx context.Context, d *Dashboard) error
	GetByID(ctx context.Context, companyID, id string) (*Dashboard, error)
	ListByCompany(ctx context.Context, companyID string) ([]*Dashboard, error)
	Delete(ctx context.Context, companyID, id string) error
}
