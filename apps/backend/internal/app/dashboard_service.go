package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DashboardService is the CRUD surface for native dashboards (T-D6).
//
// Its one interesting rule is the asymmetry in validate-on-save: **refuse on
// structure, warn on execution.** A structural mistake is the author's and is
// the same mistake every time the dashboard loads, so it is refused where
// somebody can read the message. An execution failure depends on the data on the
// day — and the metric registry's live gate is the evidence for that
// (docs/coverage/metric-registry.md §4: SUM over an empty window returns NULL,
// and "every metric in this gate needed the workaround"). A dashboard is a dozen
// statements an agent wrote in a turn that is about to end, and losing eleven
// good panels because one hit a cold window is the worse failure.
type DashboardService struct {
	repo     domain.DashboardRepository
	conns    domain.ConnectionRepository
	resolver *dashboard.Resolver
	now      func() time.Time
}

func NewDashboardService(repo domain.DashboardRepository, conns domain.ConnectionRepository, resolver *dashboard.Resolver) *DashboardService {
	return &DashboardService{repo: repo, conns: conns, resolver: resolver, now: time.Now}
}

// DashboardInput is one submitted dashboard.
type DashboardInput struct {
	ThreadID    *string        `json:"thread_id,omitempty"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Spec        spec.Dashboard `json:"spec"`
}

// SaveResult carries the stored dashboard and any panel that saved with a
// warning, so the caller — an agent finishing a turn, or an admin in the UI —
// can say which tiles need a second look without having to re-read the row.
type SaveResult struct {
	Dashboard *domain.Dashboard `json:"dashboard"`
	Warnings  []PanelWarning    `json:"warnings,omitempty"`
}

// PanelWarning is a panel that is structurally sound and did not answer when it
// was saved.
type PanelWarning struct {
	PanelID string `json:"panel_id"`
	Message string `json:"message"`
}

// List returns the company's dashboards.
func (s *DashboardService) List(ctx context.Context, companyID string) ([]*domain.Dashboard, error) {
	out, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*domain.Dashboard{}
	}
	return out, nil
}

// Get returns one dashboard, scoped to the company in the WHERE clause.
func (s *DashboardService) Get(ctx context.Context, companyID, id string) (*domain.Dashboard, error) {
	return s.repo.GetByID(ctx, companyID, id)
}

// Create validates and stores a dashboard, executing every panel first.
func (s *DashboardService) Create(ctx context.Context, companyID, createdBy string, in DashboardInput) (*SaveResult, error) {
	d, err := s.validated(ctx, companyID, in, nil)
	if err != nil {
		return nil, err
	}
	if createdBy != "" {
		d.CreatedBy = &createdBy
	}
	warnings, err := s.dryRun(ctx, companyID, d)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, d); err != nil {
		return nil, err
	}
	return &SaveResult{Dashboard: d, Warnings: warnings}, nil
}

// Update rewrites a dashboard the company owns.
func (s *DashboardService) Update(ctx context.Context, companyID, id string, in DashboardInput) (*SaveResult, error) {
	current, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	d, err := s.validated(ctx, companyID, in, current)
	if err != nil {
		return nil, err
	}
	d.ID, d.CreatedBy, d.CreatedAt = current.ID, current.CreatedBy, current.CreatedAt
	warnings, err := s.dryRun(ctx, companyID, d)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, d); err != nil {
		return nil, err
	}
	return &SaveResult{Dashboard: d, Warnings: warnings}, nil
}

// Delete removes a dashboard the company owns.
func (s *DashboardService) Delete(ctx context.Context, companyID, id string) error {
	return s.repo.Delete(ctx, companyID, id)
}

// Resolve runs a stored dashboard for a viewer.
func (s *DashboardService) Resolve(ctx context.Context, companyID, id string, req map[string]string) (*dashboard.Result, error) {
	d, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	if s.resolver == nil {
		return nil, fmt.Errorf("this deployment has no dashboard resolver wired")
	}
	return s.resolver.Resolve(ctx, companyID, d, req)
}

// validated turns an input into a row, refusing everything structural.
func (s *DashboardService) validated(ctx context.Context, companyID string, in DashboardInput, current *domain.Dashboard) (*domain.Dashboard, error) {
	sp := in.Spec
	if strings.TrimSpace(sp.Title) == "" {
		sp.Title = strings.TrimSpace(in.Title)
	}
	if sp.SpecVersion == 0 {
		sp.SpecVersion = spec.Version
	}
	if err := spec.Validate(&sp); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	// The source must be one of this company's own connections. The spec was
	// submitted by a caller — an agent turn, an API client — and a stored
	// dashboard must not be a latent cross-tenant read waiting for a resolver
	// bug. Checked here as well as in the resolver, for the same reason
	// MetricService.evaluate checks twice.
	owned, err := s.conns.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if !ownsSource(owned, sp.SourceID) {
		return nil, fmt.Errorf("%w: source %s is not one of this company's connections", domain.ErrInvalidInput, sp.SourceID)
	}

	d := &domain.Dashboard{
		CompanyID:   companyID,
		ThreadID:    in.ThreadID,
		SourceID:    sp.SourceID,
		Title:       sp.Title,
		Description: strings.TrimSpace(in.Description),
		Spec:        sp,
		SpecVersion: sp.SpecVersion,
	}
	if sp.RefreshSecs > 0 {
		secs := sp.RefreshSecs
		d.RefreshSecs = &secs
	}
	if current != nil && in.ThreadID == nil {
		d.ThreadID = current.ThreadID
	}
	return d, nil
}

// dryRun executes every panel once so a dashboard that cannot answer is known
// before it reaches a grid. Execution failures come back as warnings; only a
// failure of the resolve itself — a bad filter set, an unreachable source — is
// an error, because that one is not per-panel and would fail identically for
// every viewer.
func (s *DashboardService) dryRun(ctx context.Context, companyID string, d *domain.Dashboard) ([]PanelWarning, error) {
	if s.resolver == nil {
		return nil, nil
	}
	res, err := s.resolver.Resolve(ctx, companyID, d, nil)
	if err != nil {
		return nil, err
	}
	var warnings []PanelWarning
	for _, p := range res.Panels {
		if p == nil || p.Error == "" {
			continue
		}
		msg := p.Error
		// Name the window it ran over. A panel that fails on a preset the author
		// never thought about reads as broken; the same message with the dates in
		// it reads as "there is no data there yet", which is usually what it is.
		if len(res.Windows) > 0 {
			msg += " (" + windowPhrase(res) + ")"
		}
		warnings = append(warnings, PanelWarning{PanelID: p.PanelID, Message: msg})
	}
	return warnings, nil
}

func windowPhrase(res *dashboard.Result) string {
	parts := make([]string, 0, len(res.Windows))
	for name, w := range res.Windows {
		parts = append(parts, fmt.Sprintf("%s %s→%s", name,
			w.From.Format(dashboard.DateLayout), w.To.Format(dashboard.DateLayout)))
	}
	return "ran over " + strings.Join(parts, ", ")
}

func ownsSource(owned []*domain.DBConnection, sourceID string) bool {
	for _, c := range owned {
		if c.ID == sourceID {
			return true
		}
	}
	return false
}
