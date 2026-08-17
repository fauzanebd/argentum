package dashboard

import (
	"time"

	"github.com/fauzanebd/argentum/internal/dashboard/spec"
)

// Result is one resolve: every panel's answer, plus what the filters actually
// resolved to, so the page can say which window it is showing rather than
// leaving the reader to assume it is the one they picked last week.
type Result struct {
	DashboardID string                 `json:"dashboard_id"`
	Title       string                 `json:"title"`
	Applied     map[string]string      `json:"applied_filters,omitempty"`
	Windows     map[string]spec.Window `json:"windows,omitempty"`
	Panels      []*spec.Resolved       `json:"panels"`
	ResolvedAt  time.Time              `json:"resolved_at"`
}
