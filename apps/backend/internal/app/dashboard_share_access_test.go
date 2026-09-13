package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z5: a share link is not a grant and does not consult one — so a restricted
// dashboard cannot be shared, and a link does not open one.

// shareDashboards is DashboardRepository's company-scoped read, and nothing
// else: the share service must not reach a write.
type shareDashboards struct {
	domain.DashboardRepository
	rows map[string]*domain.Dashboard
}

func (d shareDashboards) GetByID(_ context.Context, companyID, id string) (*domain.Dashboard, error) {
	if row := d.rows[id]; row != nil && row.CompanyID == companyID {
		return row, nil
	}
	return nil, domain.ErrNotFound
}

// recordingShares remembers what was written. MarkViewed is not implemented, so
// a refused Open that went on to count a view panics.
type recordingShares struct {
	domain.DashboardShareRepository
	inserted  []*domain.DashboardShare
	insertErr error
	byHash    map[string]*domain.DashboardShare
}

func (s *recordingShares) Insert(_ context.Context, sh *domain.DashboardShare) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.inserted = append(s.inserted, sh)
	return nil
}

func (s *recordingShares) ByTokenHash(_ context.Context, hash string) (*domain.DashboardShare, error) {
	if sh := s.byHash[hash]; sh != nil {
		return sh, nil
	}
	return nil, domain.ErrNotFound
}

func payrollAndSales() shareDashboards {
	return shareDashboards{rows: map[string]*domain.Dashboard{
		"payroll": {ID: "payroll", CompanyID: "co-1", Title: "Payroll", AccessMode: domain.AccessModeRestricted},
		"sales":   {ID: "sales", CompanyID: "co-1", Title: "Sales", AccessMode: domain.AccessModeOpen},
		// A mode the CHECK should have refused. Nobody decided it, so it is read
		// as closed — authz.Evaluate's rule, kept here too.
		"unknown": {ID: "unknown", CompanyID: "co-1", Title: "Unknown", AccessMode: ""},
	}}
}

func TestARestrictedDashboardCannotBeShared(t *testing.T) {
	ctx := context.Background()
	for _, id := range []string{"payroll", "unknown"} {
		shares := &recordingShares{}
		svc := NewDashboardShareService(shares, payrollAndSales(), nil, nil)
		// A pinned filter the dashboard does not declare would be refused too.
		// The restriction is the answer, because it is the reason.
		_, err := svc.Create(ctx, "co-1", "u-dewi", CreateShareInput{DashboardID: id, LockedParams: map[string]string{"region": "Jakarta"}})
		if !errors.Is(err, domain.ErrDashboardRestricted) {
			t.Errorf("sharing %s = %v, want ErrDashboardRestricted", id, err)
		}
		if len(shares.inserted) != 0 {
			t.Errorf("sharing %s wrote %d links", id, len(shares.inserted))
		}
	}

	shares := &recordingShares{}
	svc := NewDashboardShareService(shares, payrollAndSales(), nil, nil)
	if _, err := svc.Create(ctx, "co-1", "u-dewi", CreateShareInput{DashboardID: "sales"}); err != nil {
		t.Fatalf("sharing an open dashboard: %v", err)
	}
	if len(shares.inserted) != 1 {
		t.Errorf("sharing an open dashboard wrote %d links, want 1", len(shares.inserted))
	}
}

// The read said open and the row write said restricted: an admin restricted it
// in between. The repository's answer is the one that stands.
func TestAShareRestrictedBetweenTheReadAndTheWriteIsRefused(t *testing.T) {
	shares := &recordingShares{insertErr: domain.ErrDashboardRestricted}
	svc := NewDashboardShareService(shares, payrollAndSales(), nil, nil)
	_, err := svc.Create(context.Background(), "co-1", "u-dewi", CreateShareInput{DashboardID: "sales"})
	if !errors.Is(err, domain.ErrDashboardRestricted) {
		t.Errorf("Create = %v, want the write's ErrDashboardRestricted", err)
	}
}

func TestALinkDoesNotOpenARestrictedDashboard(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"payroll", "unknown"} {
		token := "tok-" + id
		shares := &recordingShares{byHash: map[string]*domain.DashboardShare{
			// Live by every measure a link has — not revoked, not expired, no
			// password. Only its dashboard's mode stands between it and the page.
			auth.HashShareToken(token): {ID: "sh-" + id, CompanyID: "co-1", DashboardID: id, ExpiresAt: now.Add(24 * time.Hour)},
		}}
		svc := NewDashboardShareService(shares, payrollAndSales(), nil, nil).WithClock(func() time.Time { return now })
		out, err := svc.Open(context.Background(), token, "", nil)
		if !errors.Is(err, ErrShareGone) || out != nil {
			t.Errorf("opening a link to %s = (%v, %v), want ErrShareGone — the answer a revoked link gets", id, out, err)
		}
	}
}
