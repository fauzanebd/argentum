package domain

import (
	"context"
	"time"
)

// MetricGrain is the natural period one metric value covers. It is what a
// comparison is measured in ("previous period" = one grain back) and what a
// watcher evaluates on.
type MetricGrain string

const (
	MetricGrainDay     MetricGrain = "day"
	MetricGrainWeek    MetricGrain = "week"
	MetricGrainMonth   MetricGrain = "month"
	MetricGrainQuarter MetricGrain = "quarter"
	MetricGrainYear    MetricGrain = "year"
)

// Valid reports whether g is a grain this release understands. Anything else is
// a rejected save rather than a surprise when a comparison tries to step back
// one period and finds it does not know how long a period is.
func (g MetricGrain) Valid() bool {
	switch g {
	case MetricGrainDay, MetricGrainWeek, MetricGrainMonth, MetricGrainQuarter, MetricGrainYear:
		return true
	}
	return false
}

// MetricUnit decides how a value reads: whether it is money, a headcount, a
// rate, or a bare ratio.
type MetricUnit string

const (
	MetricUnitCurrency MetricUnit = "currency"
	MetricUnitCount    MetricUnit = "count"
	MetricUnitPercent  MetricUnit = "percent"
	MetricUnitRatio    MetricUnit = "ratio"
)

// Valid reports whether u is a unit this release understands.
func (u MetricUnit) Valid() bool {
	switch u {
	case MetricUnitCurrency, MetricUnitCount, MetricUnitPercent, MetricUnitRatio:
		return true
	}
	return false
}

// MetricDefinition is one named, validated, parameterised number (T-06).
//
// It exists so the same question returns the same answer twice: instead of the
// agent re-deriving SQL for "revenue last month" on every turn, it runs this
// definition's template through query_metric with the window bound as
// parameters. v1 is one number, one source, one window — no dimensions, joins,
// or DSL.
type MetricDefinition struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	SourceID  string `json:"source_id"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	// Description is what the agent reads to decide whether this metric answers
	// the question, so it is specific rather than decorative.
	Description string `json:"description"`
	// SQLTemplate is a single SELECT that must contain {{from}} and {{to}}. Those
	// tokens are bound as query parameters at run time, never interpolated — the
	// property that keeps a window value from becoming an injection. Stored with
	// the tokens intact; the dialect's placeholder syntax is applied when it runs.
	SQLTemplate string `json:"sql_template"`
	// ValueColumn names the column of the single result row that carries the
	// number. Named rather than positional so a template may select more than
	// the value and stay unambiguous.
	ValueColumn string      `json:"value_column"`
	Grain       MetricGrain `json:"grain"`
	Unit        MetricUnit  `json:"unit"`
	// Currency is set when Unit is currency, empty otherwise.
	Currency string `json:"currency,omitempty"`
	// HigherIsBetter is whether a rise is good news — revenue up is good, churn
	// up is not. A delta and a watcher both read it to decide which way alarms.
	HigherIsBetter bool `json:"higher_is_better"`
	Enabled        bool `json:"enabled"`
	// CreatedBy is the admin who defined it, or empty. Unreferenced: a metric
	// outlives the user who wrote it.
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MetricRepository is the persistence contract for the registry.
//
// Every method that names a metric takes the company id beside it, like
// MCPServerRepository and for the same reason: the id is a bare uuid on an
// admin-only CRUD surface, and a repository that will answer for any company is
// one forgotten check from a cross-tenant read.
type MetricRepository interface {
	Create(ctx context.Context, m *MetricDefinition) error
	GetByID(ctx context.Context, companyID, id string) (*MetricDefinition, error)
	// GetByKey resolves the handle query_metric is given. ErrNotFound when the
	// company has no metric under that key.
	GetByKey(ctx context.Context, companyID, key string) (*MetricDefinition, error)
	ListByCompany(ctx context.Context, companyID string) ([]*MetricDefinition, error)
	// ListEnabled is what the turn catalog and the tools read: only metrics an
	// admin has left switched on reach the agent.
	ListEnabled(ctx context.Context, companyID string) ([]*MetricDefinition, error)
	Update(ctx context.Context, m *MetricDefinition) error
	Delete(ctx context.Context, companyID, id string) error
}
