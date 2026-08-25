package domain

import (
	"context"
	"time"
)

// DashboardShare is a bearer credential for one native dashboard (T-D13).
//
// **It is the only object in this product where an unauthenticated request
// causes a query against a customer's production database.** A report share
// serves a rendered artefact that was produced once; this one runs live SQL for
// whoever holds the link. Every field below that is not on `ReportShare` exists
// because of that difference.
type DashboardShare struct {
	ID          string `json:"id"`
	CompanyID   string `json:"company_id"`
	DashboardID string `json:"dashboard_id"`
	TokenHash   string `json:"-"`

	// LockedParams are pinned filter values, and they are **locked, never
	// merged**. A dashboard shared with `region` pinned to Jakarta shows
	// Jakarta, and a visitor who edits the query string still sees Jakarta,
	// because request parameters on a share are ignored rather than merged.
	//
	// Merging is the obvious implementation and it is the one that turns every
	// declared filter into a dimension a stranger may enumerate: a link to one
	// region becomes a link to all of them by typing.
	LockedParams map[string]string `json:"locked_params,omitempty"`

	// AllowFilters says whether the visitor may move the filters that are not
	// pinned. False by default, which is the safe end: a share is a picture of
	// something specific until somebody decides otherwise.
	AllowFilters bool `json:"allow_filters"`

	// PasswordHash is Argon2id, not SHA-256, and the difference from TokenHash
	// is deliberate. A token is 256 uniformly random bits with no dictionary
	// behind it; a password is human-chosen and has one. The primitive follows
	// the entropy of the input, not the sensitivity of what it guards.
	PasswordHash string `json:"-"`

	// MaxRefreshPerHour bounds what one link may spend of a customer's
	// warehouse. A bearer link that can refresh without limit is a leaked link
	// that costs money forever.
	MaxRefreshPerHour int `json:"max_refresh_per_hour"`

	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`

	ViewCount    int        `json:"view_count"`
	LastViewedAt *time.Time `json:"last_viewed_at,omitempty"`
}

// Live reports whether a share still opens. Same rule as ReportShare.Live and
// deliberately the same shape: expiry bounds the link nobody remembers,
// revocation is the button pressed at 11pm, and a share needs both.
func (s *DashboardShare) Live(now time.Time) bool {
	return s != nil && s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// RequiresPassword reports whether opening this link needs one.
func (s *DashboardShare) RequiresPassword() bool {
	return s != nil && s.PasswordHash != ""
}

// EffectiveParams is what the resolver runs, given what the visitor asked for.
//
// **The pinned values always win and the request never adds a key.** When
// AllowFilters is false the request is ignored entirely. When it is true a
// visitor may move a filter the share did not pin — and may not touch one it
// did, and may not introduce a name the share never mentioned, because a filter
// this dashboard does not declare is not a filter at all.
func (s *DashboardShare) EffectiveParams(requested map[string]string) map[string]string {
	out := make(map[string]string, len(s.LockedParams)+len(requested))
	if s.AllowFilters {
		for k, v := range requested {
			if _, pinned := s.LockedParams[k]; pinned {
				continue
			}
			out[k] = v
		}
	}
	for k, v := range s.LockedParams {
		out[k] = v
	}
	return out
}

// DashboardShareLimits. A default that is not forever and a ceiling an admin
// cannot type past, in the same unit and for the same reasons as the report
// share's.
const (
	DashboardShareDefaultRefreshPerHour = 60
	DashboardShareMaxRefreshPerHour     = 600
)

// DashboardShareRepository persists dashboard share links.
type DashboardShareRepository interface {
	Insert(ctx context.Context, s *DashboardShare) error
	// ByTokenHash resolves the bearer token. It is **not** company-scoped and
	// cannot be: the visitor is logged out and has no tenant. The token is the
	// whole credential, which is why the row carries the company id —
	// everything the page then reads is scoped by what comes back from here
	// rather than by anything the request said.
	ByTokenHash(ctx context.Context, hash string) (*DashboardShare, error)
	// ListForDashboard is the link list, newest first, including revoked and
	// expired rows: a share that has disappeared from the UI is a share nobody
	// can prove was ever created.
	ListForDashboard(ctx context.Context, companyID, dashboardID string) ([]*DashboardShare, error)
	// Revoke is company-scoped and idempotent. Revoking twice is what a nervous
	// admin does and the second attempt must not be an error they interpret.
	Revoke(ctx context.Context, companyID, id string) error
	// MarkViewed bumps the counter and the timestamp. Best-effort by contract:
	// a share that opened must not fail because its statistics did not.
	MarkViewed(ctx context.Context, id string) error
}
