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

// PublicCopy returns this dashboard with everything a visitor holding a share
// link must not be given (T-D13).
//
// **Found by the 2026-09-03 gate: the share page was serving the panel SQL.**
// `/share/dashboard/:token` takes no session and no key — the token in the path
// is the whole credential — and it was returning the entire `Dashboard`, so an
// unauthenticated caller with a link received `SELECT … FROM dim_customers` for
// every panel, plus the tenant's `company_id` and `source_id`. That is the
// warehouse's table and column names, the join structure, and any literal a
// panel pins in a `WHERE` clause, handed to anyone the link reaches.
//
// It contradicted two decisions this product had already taken. `T-H7` holds
// that a statement's literals are sensitive enough to keep out of an operator's
// *log*; `T-H12` holds that `get_schema` must not so much as name a table the
// tenant excluded. A public link that publishes both is the same disclosure
// through a door nobody had looked at.
//
// **What stays.** Everything the renderer reads: title, viz, layout, the column
// mapping and the format. The panel's *result* travels beside it in
// `OpenedShare.Result`, which is the data the page draws — the SQL was never
// used by the frontend, which is why nothing broke when it stopped being sent.
//
// A copy rather than a mutation: the argument is a pointer into the repository's
// result and the caller may be holding it for something else.
func (d *Dashboard) PublicCopy() *Dashboard {
	if d == nil {
		return nil
	}
	out := *d
	out.CompanyID = ""
	out.SourceID = ""
	out.ThreadID = nil
	out.CreatedBy = nil

	// The spec is copied a level deeper because Panels is a slice: assigning
	// the struct would share the backing array, and blanking a panel's SQL
	// would blank it in the repository's copy too.
	out.Spec = d.Spec
	out.Spec.SourceID = ""
	out.Spec.Panels = make([]spec.Panel, len(d.Spec.Panels))
	copy(out.Spec.Panels, d.Spec.Panels)
	for i := range out.Spec.Panels {
		out.Spec.Panels[i].SQL = ""
		// The metric key goes too. It is a name the tenant chose for a number
		// in their own registry — less revealing than SQL and still theirs, and
		// the renderer reads the resolved value rather than the key.
		out.Spec.Panels[i].MetricKey = ""
	}
	return &out
}
