package dashboard

import (
	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Input is one submitted dashboard, whoever submitted it.
//
// It lives here rather than beside the service because internal/app depends on
// internal/tools and not the other way round, so the create_dashboard tool
// cannot name a type declared in app. Putting the shape next to the spec it
// carries keeps one definition for both callers.
type Input struct {
	ThreadID    *string        `json:"thread_id,omitempty"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Spec        spec.Dashboard `json:"spec"`
}

// SaveResult carries the stored dashboard, what it counted, and any panel that
// saved with a warning — so an agent finishing a turn, or an admin in the UI,
// can say which tiles need a second look without re-reading the row.
type SaveResult struct {
	Dashboard *domain.Dashboard `json:"dashboard"`
	Warnings  []PanelWarning    `json:"warnings,omitempty"`
	// RowCount is every row the panels returned when the dashboard was saved.
	//
	// It exists because a data tool that returns a URL and no row count is
	// half-wired: guardrails.CheckFabrication grounds a reply on
	// TurnEvidence.DataRows > 0, and the metric registry's gate recorded what
	// happens when a tool omits it — every metric-only answer was suppressed as
	// a fabrication while the audit row logged rows_returned = NULL.
	RowCount int `json:"row_count"`
}

// PanelWarning is a panel that is structurally sound and did not answer when it
// was saved.
type PanelWarning struct {
	PanelID string `json:"panel_id"`
	Message string `json:"message"`
}
