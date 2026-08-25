package dashboard

import (
	"context"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// QueryLogger records what a dashboard ran against a tenant's warehouse (T-D9).
//
// It is its own log rather than rows in `agent_actions`, on two arguments.
//
// First, `WithAudit` decorates `interfaces.Tool`, and its comment says the
// decorator exists so a tool written next year is audited without its author
// knowing that package exists. A share-page render and a scheduled refresh are
// not tool calls — there is no Tool to decorate — so writing into
// `agent_actions` from here would be a second write path into a table whose
// design is one row per tool execution written in one place, which is the
// property that makes the audit endpoint trustworthy.
//
// Second, and decisively, the retention obligations are opposite.
// `agent_actions.args_redacted` holds redacted arguments by design, which is
// exactly why T-H6 exempts audit rows from erasure: they carry no tenant
// content and should outlive conversations. This log stores the rendered
// statement verbatim, literals included. Putting it in `agent_actions` forces
// one of two bad outcomes — redact it and lose the question "what ran against
// my database last month", or keep it and void T-H6's exemption for the whole
// table.
type QueryLogger interface {
	LogQuery(ctx context.Context, e QueryLogEntry)
}

// QueryLogEntry is one execution against a tenant warehouse.
type QueryLogEntry struct {
	CompanyID   string
	DashboardID string
	PanelID     string
	SourceID    string
	// ActorKind mirrors agent_actions' vocabulary — user | share | schedule —
	// so the two logs read side by side.
	ActorKind  string
	ActorRef   string
	SQLText    string
	Params     map[string]any
	RowCount   int
	Status     string
	Error      string
	DurationMS int
}

// Status values, matching the column's comment.
const (
	QueryStatusOK        = "ok"
	QueryStatusError     = "error"
	QueryStatusTruncated = "truncated"
)

// Actor kinds. `share` is the one this log exists to make visible: a share-page
// render is an unauthenticated read of a customer's warehouse, and before T-D9
// it left no trace anywhere.
const (
	ActorKindUser     = "user"
	ActorKindShare    = "share"
	ActorKindSchedule = "schedule"
)

// logQuery writes one row, if this deployment wired a logger. Failures are the
// logger's to swallow: a dashboard must not fail because its audit trail did.
func (r *Resolver) logQuery(ctx context.Context, d *domain.Dashboard, p *spec.Panel, sql string, params *Params, res *db.QueryResult, execErr error, took time.Duration) {
	if r.log == nil {
		return
	}
	kind, ref := tenantctx.Actor(ctx)
	if kind == "" {
		kind = ActorKindUser
	}
	e := QueryLogEntry{
		CompanyID:   d.CompanyID,
		DashboardID: d.ID,
		PanelID:     p.ID,
		SourceID:    d.SourceID,
		ActorKind:   kind,
		ActorRef:    ref,
		SQLText:     sql,
		DurationMS:  int(took.Milliseconds()),
		Status:      QueryStatusOK,
	}
	if params != nil {
		e.Params = params.Values
	}
	switch {
	case execErr != nil:
		e.Status = QueryStatusError
		e.Error = execErr.Error()
	case res == nil:
		e.Status = QueryStatusError
		e.Error = "no result and no error"
	default:
		e.RowCount = res.Count
		if res.Truncated {
			e.Status = QueryStatusTruncated
		}
	}
	r.log.LogQuery(ctx, e)
}
