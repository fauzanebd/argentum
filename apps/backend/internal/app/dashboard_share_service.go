package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// DashboardShareService mints, lists, revokes and opens dashboard share links
// (T-D13).
//
// **This is the only service in the product where an unauthenticated caller
// causes a query against a customer's production database.** A report share
// serves an artefact rendered once; this one runs live SQL for a stranger. The
// three rules that follow from that are enforced here rather than left to
// handlers: the company is read off the row and never off the request, pinned
// parameters are locked rather than merged, and a link's spend is bounded.
type DashboardShareService struct {
	shares     domain.DashboardShareRepository
	dashboards domain.DashboardRepository
	resolver   *dashboard.Resolver
	rdb        *redis.Client
	now        func() time.Time
}

func NewDashboardShareService(
	shares domain.DashboardShareRepository,
	dashboards domain.DashboardRepository,
	resolver *dashboard.Resolver,
	rdb *redis.Client,
) *DashboardShareService {
	return &DashboardShareService{
		shares:     shares,
		dashboards: dashboards,
		resolver:   resolver,
		rdb:        rdb,
		now:        time.Now,
	}
}

// WithClock injects a clock for tests.
func (s *DashboardShareService) WithClock(now func() time.Time) *DashboardShareService {
	if now != nil {
		s.now = now
	}
	return s
}

// CreateShareInput is what an admin chooses when minting a link.
type CreateShareInput struct {
	DashboardID       string
	ExpiresInDays     int
	LockedParams      map[string]string
	AllowFilters      bool
	Password          string
	MaxRefreshPerHour int
}

// CreatedDashboardShare carries the token, which exists in plaintext exactly
// once — in this return value. It is never stored and cannot be recovered.
type CreatedDashboardShare struct {
	Share *domain.DashboardShare `json:"share"`
	Token string                 `json:"token"`
}

func (s *DashboardShareService) Create(ctx context.Context, companyID, userID string, in CreateShareInput) (*CreatedDashboardShare, error) {
	// The dashboard is read company-scoped first, so a share cannot be minted
	// for another tenant's dashboard by id.
	d, err := s.dashboards.GetByID(ctx, companyID, in.DashboardID)
	if err != nil {
		return nil, err
	}

	days := in.ExpiresInDays
	if days <= 0 {
		days = domain.ShareDefaultDays
	}
	if days > domain.ShareMaxDays {
		return nil, fmt.Errorf("%w: a share may not last more than %d days", domain.ErrInvalidInput, domain.ShareMaxDays)
	}

	refresh := in.MaxRefreshPerHour
	if refresh <= 0 {
		refresh = domain.DashboardShareDefaultRefreshPerHour
	}
	if refresh > domain.DashboardShareMaxRefreshPerHour {
		return nil, fmt.Errorf("%w: a share may not refresh more than %d times an hour",
			domain.ErrInvalidInput, domain.DashboardShareMaxRefreshPerHour)
	}

	// Pinned parameters must name filters the dashboard actually declares.
	// A pin on a name the spec never mentions is a typo that would silently do
	// nothing, and the admin would believe the link was narrower than it is.
	declared := map[string]bool{}
	for _, f := range d.Spec.Filters {
		declared[f.Name] = true
	}
	for k := range in.LockedParams {
		if !declared[k] {
			return nil, fmt.Errorf("%w: this dashboard declares no filter named %q", domain.ErrInvalidInput, k)
		}
	}

	token, hash, err := auth.NewShareToken()
	if err != nil {
		return nil, fmt.Errorf("mint share token: %w", err)
	}

	sh := &domain.DashboardShare{
		CompanyID:         companyID,
		DashboardID:       d.ID,
		TokenHash:         hash,
		LockedParams:      in.LockedParams,
		AllowFilters:      in.AllowFilters,
		MaxRefreshPerHour: refresh,
		CreatedBy:         userID,
		ExpiresAt:         s.now().UTC().Add(time.Duration(days) * 24 * time.Hour),
	}
	// Argon2id, not the token's SHA-256: a human-chosen password has a
	// dictionary behind it and the primitive follows the entropy of the input.
	if p := strings.TrimSpace(in.Password); p != "" {
		ph, err := auth.HashPassword(p)
		if err != nil {
			return nil, fmt.Errorf("hash share password: %w", err)
		}
		sh.PasswordHash = ph
	}

	if err := s.shares.Insert(ctx, sh); err != nil {
		return nil, err
	}
	return &CreatedDashboardShare{Share: sh, Token: token}, nil
}

func (s *DashboardShareService) List(ctx context.Context, companyID, dashboardID string) ([]*domain.DashboardShare, error) {
	return s.shares.ListForDashboard(ctx, companyID, dashboardID)
}

func (s *DashboardShareService) Revoke(ctx context.Context, companyID, shareID string) error {
	return s.shares.Revoke(ctx, companyID, shareID)
}

// ErrShareGone is deliberately the report player's, not a second one: the two
// surfaces answer a bad token identically, and one variable makes that a fact
// rather than a coincidence two handlers have to keep agreeing on.
//
// ErrSharePassword is returned when a link needs a password and the one
// supplied was absent or wrong.
//
// Distinct from ErrShareGone on purpose, and it is a narrower disclosure than
// it looks: the visitor already holds a valid token, so learning that it wants
// a password reveals nothing they could not infer from the page they were sent.
var ErrSharePassword = errors.New("this share needs a password")

// ErrShareBudget is a link that has spent its hour's refreshes.
var ErrShareBudget = errors.New("this share has been refreshed too many times this hour")

// OpenedShare is a resolved dashboard, ready to render.
type OpenedShare struct {
	Share     *domain.DashboardShare `json:"share"`
	Dashboard *domain.Dashboard      `json:"dashboard"`
	Result    *dashboard.Result      `json:"result"`
}

// Open turns a bearer token into a rendered dashboard.
//
// The token is the only thing the visitor supplies that is trusted. The
// company, the dashboard and the parameters all come out of the share row, and
// the request's own query string reaches the resolver only through
// EffectiveParams — which ignores it entirely unless the share allows filters,
// and never lets it override a pin.
func (s *DashboardShareService) Open(ctx context.Context, token, password string, requested map[string]string) (*OpenedShare, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrShareGone
	}
	sh, err := s.shares.ByTokenHash(ctx, auth.HashShareToken(token))
	if err != nil || !sh.Live(s.now()) {
		return nil, ErrShareGone
	}

	if sh.RequiresPassword() {
		ok, err := auth.VerifyPassword(password, sh.PasswordHash)
		if err != nil || !ok {
			return nil, ErrSharePassword
		}
	}

	if err := s.spend(ctx, sh); err != nil {
		return nil, err
	}

	// Company off the row. The visitor asserted nothing and is believed about
	// nothing except the token.
	d, err := s.dashboards.GetByID(ctx, sh.CompanyID, sh.DashboardID)
	if err != nil {
		return nil, ErrShareGone
	}

	// The turn is attributed as `share` so T-D9's log can answer "what did the
	// public links run against my warehouse", which before this ticket was a
	// question with no record behind it anywhere.
	ctx = tenantctx.WithCompanyID(ctx, sh.CompanyID)
	ctx = tenantctx.WithActor(ctx, dashboard.ActorKindShare, sh.ID)

	res, err := s.resolver.Resolve(ctx, sh.CompanyID, d, sh.EffectiveParams(requested))
	if err != nil {
		return nil, err
	}

	if err := s.shares.MarkViewed(ctx, sh.ID); err != nil {
		// Best effort by contract: a page that opened must not fail because its
		// counter did not.
		logrus.WithError(err).WithField("share_id", sh.ID).Debug("dashboard share view counter not updated")
	}
	// The visitor gets the redacted dashboard, never the stored one: the panel
	// SQL, the source and the company id are ours, and this route has no
	// session behind it. See Dashboard.PublicCopy.
	return &OpenedShare{Share: sh, Dashboard: d.PublicCopy(), Result: res}, nil
}

// spend enforces MaxRefreshPerHour with a Redis counter on a rolling hour.
//
// **Fails open when there is no Redis, and that is a decision rather than an
// oversight.** The alternative — refusing to serve a valid share because a
// counter is unavailable — turns an infrastructure blip into a customer's
// dashboard going dark for everyone holding a link. The cap exists to bound
// cost on a leaked link, not to be a security control, and the security control
// (expiry, revocation, the token itself) does not depend on Redis.
func (s *DashboardShareService) spend(ctx context.Context, sh *domain.DashboardShare) error {
	if s.rdb == nil || sh.MaxRefreshPerHour <= 0 {
		return nil
	}
	key := fmt.Sprintf("dash:share:spend:%s:%d", sh.ID, s.now().UTC().Unix()/3600)
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		logrus.WithError(err).Debug("share refresh budget not counted; serving anyway")
		return nil
	}
	if n == 1 {
		// Expire slightly past the hour so the final increments of a bucket
		// cannot outlive their window's meaning.
		s.rdb.Expire(ctx, key, 70*time.Minute)
	}
	if n > int64(sh.MaxRefreshPerHour) {
		return ErrShareBudget
	}
	return nil
}
