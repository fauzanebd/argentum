package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/dashboard"
)

// DashboardQueryLogRepo writes T-D9's record of what ran against a tenant
// warehouse.
type DashboardQueryLogRepo struct{ db *sql.DB }

func NewDashboardQueryLogRepo(db *sql.DB) *DashboardQueryLogRepo {
	return &DashboardQueryLogRepo{db: db}
}

// LogQuery writes one row and never returns an error, because there is no
// caller who could do anything useful with one: the panel has already run and
// its answer is already correct. A failure here is logged at Warn and the
// dashboard renders.
//
// The same trade the audit decorator makes ("agent action audit write failed;
// the call itself succeeded"), for the same reason and stated the same way, so
// the two logs fail alike.
func (r *DashboardQueryLogRepo) LogQuery(ctx context.Context, e dashboard.QueryLogEntry) {
	params := []byte("{}")
	if len(e.Params) > 0 {
		if b, err := json.Marshal(e.Params); err == nil {
			params = b
		}
	}
	// dashboard_id is nullable and carries no FK: a log row outlives its
	// dashboard, and an ad-hoc resolve has no dashboard at all.
	var dashboardID any
	if e.DashboardID != "" {
		dashboardID = e.DashboardID
	}

	const q = `
		INSERT INTO dashboard_query_log
			(company_id, dashboard_id, panel_id, source_id, actor_kind, actor_ref,
			 sql_text, params, row_count, status, error, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	if _, err := r.db.ExecContext(ctx, q,
		e.CompanyID, dashboardID, e.PanelID, e.SourceID, e.ActorKind, e.ActorRef,
		e.SQLText, params, e.RowCount, e.Status, truncateLogError(e.Error), e.DurationMS,
	); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id":   e.CompanyID,
			"dashboard_id": e.DashboardID,
			"panel_id":     e.PanelID,
		}).Warn("dashboard query log write failed; the panel itself succeeded")
	}
}

// maxLogErrorBytes bounds the stored error. A driver can return a message
// carrying the whole statement back, and this column already stores the
// statement once.
const maxLogErrorBytes = 2000

func truncateLogError(s string) string {
	if len(s) <= maxLogErrorBytes {
		return s
	}
	return s[:maxLogErrorBytes] + "…"
}
