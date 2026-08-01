package metric

import (
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Evaluation is the result of running a metric over one window: the number, the
// window it covered, and the SQL it ran (for the dashboard's Test button and a
// support trace). It lives in this package rather than in the app service so the
// query_metric tool (internal/tools) can name it without importing internal/app,
// which would be a cycle.
type Evaluation struct {
	Value       float64   `json:"value"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	RenderedSQL string    `json:"rendered_sql"`
}

// Result is what query_metric returns (T-07): the primary value, and — when a
// comparison was asked for — the comparison value plus the delta between them.
type Result struct {
	Metric     *domain.MetricDefinition
	Primary    Evaluation
	Comparison *Evaluation
	// Delta and DeltaPct are Primary − Comparison, present only when a comparison
	// ran. DeltaPct is nil when the comparison value was zero (a percentage
	// change from nothing is not a number).
	Delta    *float64
	DeltaPct *float64
}
